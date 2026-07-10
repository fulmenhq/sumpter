// Package valueprofile implements the guarded run-level value-domain profile
// (SUM-041 fold / B2.6). Emission is opt-in; when enabled the default-deny
// enumeration guard is mandatory (ships guarded or not at all).
package valueprofile

import (
	"fmt"
	"strings"
)

const (
	// ProfileVersion is the wire version of the diagnostic profile object.
	ProfileVersion = "sumpter.value-profile/v0"

	// DefaultMaxDistinct is the per-field Tier-A cardinality cap.
	DefaultMaxDistinct = 100
	// HardMaxDistinct is the absolute ceiling on max_distinct (bounded memory).
	HardMaxDistinct = 10000
	// DefaultSmallCellThreshold suppresses quasi/linkage aggregate cells
	// below this count (k-anonymity floor).
	DefaultSmallCellThreshold = 5

	TierEnumeration = "enumeration"
	TierAggregates  = "aggregates"

	StatusComplete              = "complete"
	StatusHighCardinalityCapped = "high_cardinality_capped"

	SensitivityPublic     = "public"
	SensitivityInternal   = "internal"
	SensitivityControlled = "controlled"
	SensitivityRestricted = "restricted"
	SensitivityUnknown    = "unknown"

	TagSafeToProfile         = "safe_to_profile"
	TagSourceStructure       = "source_structure"
	TagDirectIdentifier      = "direct_identifier"
	TagQuasiIdentifier       = "quasi_identifier"
	TagLinkageKey            = "linkage_key"
	TagMeasure               = "measure"
	TagAccessControlMetadata = "access_control_metadata"
	TagFreeformText          = "freeform_text"
	TagOpaquePayload         = "opaque_payload"
	ShapeOpaqueString        = "opaque_string"
	ShapeAllNumeric          = "all_numeric"
	ShapeUUIDShaped          = "uuid_shaped"
	ShapeEmailShaped         = "email_shaped"
	ShapeFreeform            = "freeform"
)

// closedProtectionTags is the data-artifact/v0 protection_tag vocabulary.
// Unknown tags fail closed so a typo cannot silently skip opaque_string.
var closedProtectionTags = map[string]struct{}{
	TagSafeToProfile:         {},
	TagSourceStructure:       {},
	TagDirectIdentifier:      {},
	TagQuasiIdentifier:       {},
	TagLinkageKey:            {},
	TagMeasure:               {},
	TagAccessControlMetadata: {},
	TagFreeformText:          {},
	TagOpaquePayload:         {},
}

// Config is the opt-in recipe/CLI surface for value profiling.
type Config struct {
	Enabled            bool          `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	MaxDistinct        int           `yaml:"max_distinct,omitempty" json:"max_distinct,omitempty"`
	SmallCellThreshold int           `yaml:"small_cell_threshold,omitempty" json:"small_cell_threshold,omitempty"`
	Fields             []FieldConfig `yaml:"fields,omitempty" json:"fields,omitempty"`
}

// FieldConfig names a field to observe and optional gate metadata used by the
// default-deny enumeration guard. Missing/empty gate fields → Tier B.
type FieldConfig struct {
	Field          string   `yaml:"field" json:"field"`
	SafeToProfile  bool     `yaml:"safe_to_profile,omitempty" json:"safe_to_profile,omitempty"`
	Sensitivity    string   `yaml:"sensitivity,omitempty" json:"sensitivity,omitempty"`
	ProtectionTags []string `yaml:"protection_tags,omitempty" json:"protection_tags,omitempty"`
}

// Normalize applies defaults and validates the config. Returns a copy safe for
// use. Enabled=false or empty Fields yields a no-op config.
func (c Config) Normalize() (Config, error) {
	out := c
	if out.MaxDistinct <= 0 {
		out.MaxDistinct = DefaultMaxDistinct
	}
	if out.MaxDistinct > HardMaxDistinct {
		return Config{}, fmt.Errorf("value_profile.max_distinct %d exceeds hard maximum %d", out.MaxDistinct, HardMaxDistinct)
	}
	if out.SmallCellThreshold <= 0 {
		out.SmallCellThreshold = DefaultSmallCellThreshold
	}
	seen := make(map[string]struct{}, len(out.Fields))
	normalized := make([]FieldConfig, 0, len(out.Fields))
	for _, f := range out.Fields {
		name := strings.TrimSpace(f.Field)
		if name == "" {
			return Config{}, fmt.Errorf("value_profile.fields[].field is required")
		}
		if _, ok := seen[name]; ok {
			return Config{}, fmt.Errorf("value_profile.fields: duplicate field %q", name)
		}
		seen[name] = struct{}{}
		sens := strings.ToLower(strings.TrimSpace(f.Sensitivity))
		if sens == "" {
			sens = SensitivityUnknown
		}
		switch sens {
		case SensitivityPublic, SensitivityInternal, SensitivityControlled, SensitivityRestricted, SensitivityUnknown:
		default:
			return Config{}, fmt.Errorf("value_profile field %q: invalid sensitivity %q", name, f.Sensitivity)
		}
		tags, err := normalizeProtectionTags(f.ProtectionTags)
		if err != nil {
			return Config{}, fmt.Errorf("value_profile field %q: %w", name, err)
		}
		// safe_to_profile may also appear as a protection tag; keep the bool
		// authoritative for the Tier-A gate.
		if hasTag(tags, TagSafeToProfile) {
			f.SafeToProfile = true
		}
		normalized = append(normalized, FieldConfig{
			Field:          name,
			SafeToProfile:  f.SafeToProfile,
			Sensitivity:    sens,
			ProtectionTags: tags,
		})
	}
	out.Fields = normalized
	return out, nil
}

func normalizeProtectionTags(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, tag := range raw {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		// Canonical form is snake_case lowercase as in the contract enum.
		tag = strings.ToLower(tag)
		if _, ok := closedProtectionTags[tag]; !ok {
			return nil, fmt.Errorf("unknown protection_tag %q", tag)
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out, nil
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// Active reports whether profiling should run.
func (c Config) Active() bool {
	return c.Enabled && len(c.Fields) > 0
}
