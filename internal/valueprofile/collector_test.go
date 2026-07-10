package valueprofile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTierAEnumerationRequiresFullGate(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		MaxDistinct: 10,
		Fields: []FieldConfig{
			{Field: "status", SafeToProfile: true, Sensitivity: SensitivityPublic},
		},
	}
	c, err := NewCollector(cfg)
	if err != nil || c == nil {
		t.Fatalf("NewCollector: %v %#v", err, c)
	}
	for _, v := range []string{"active", "closed", "pending", "active"} {
		c.ObserveData(map[string]interface{}{"status": v})
	}
	profile := c.Snapshot()
	fr := profile.Fields["status"]
	if fr.Tier != TierEnumeration {
		t.Fatalf("tier = %q, want enumeration", fr.Tier)
	}
	if fr.Distinct == nil || (*fr.Distinct)["active"] != 2 || (*fr.Distinct)["closed"] != 1 || (*fr.Distinct)["pending"] != 1 {
		t.Fatalf("distinct = %#v", fr.Distinct)
	}
	if fr.DistinctCount != 3 {
		t.Fatalf("distinct_count = %#v, want 3", fr.DistinctCount)
	}
}

func TestDefaultDenyUntaggedIsAggregatesOnly(t *testing.T) {
	// Bootstrap: no safe_to_profile / unknown sensitivity → never Tier A.
	cfg := Config{
		Enabled: true,
		Fields: []FieldConfig{
			{Field: "account_id"}, // unknown, not safe
		},
	}
	c, err := NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	secret := "acc-0001-SENSITIVE"
	for i := 0; i < 3; i++ {
		c.ObserveData(map[string]interface{}{"account_id": secret})
	}
	profile := c.Snapshot()
	fr := profile.Fields["account_id"]
	if fr.Tier != TierAggregates {
		t.Fatalf("tier = %q, want aggregates", fr.Tier)
	}
	if fr.Distinct != nil {
		t.Fatalf("Tier-B must not emit distinct values: %#v", fr.Distinct)
	}
	// Negative: raw identifier must not appear anywhere in the profile wire shape.
	if blob := mustJSON(t, profile); strings.Contains(blob, secret) {
		t.Fatalf("sensitive value leaked into profile JSON: %s", blob)
	}
}

func TestControlledMeasureNeverEmitsNumericRange(t *testing.T) {
	cfg := Config{
		Enabled: true,
		Fields: []FieldConfig{
			{
				Field:          "balance",
				SafeToProfile:  true, // mistagged — sensitivity must win
				Sensitivity:    SensitivityControlled,
				ProtectionTags: []string{TagMeasure},
			},
		},
	}
	c, err := NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{10, 99999, 50} {
		c.ObserveData(map[string]interface{}{"balance": v})
	}
	fr := c.Snapshot().Fields["balance"]
	if fr.Tier != TierAggregates {
		t.Fatalf("tier = %q, want aggregates (sensitivity wins)", fr.Tier)
	}
	if fr.Min != nil || fr.Max != nil {
		t.Fatalf("controlled measure must not emit min/max: min=%v max=%v", fr.Min, fr.Max)
	}
	if fr.Shape != ShapeAllNumeric {
		t.Fatalf("shape = %q, want all_numeric", fr.Shape)
	}
}

func TestSourceStructureShapeIsOpaqueString(t *testing.T) {
	cfg := Config{
		Enabled: true,
		Fields: []FieldConfig{
			{
				Field:          "object_key",
				Sensitivity:    SensitivityRestricted,
				ProtectionTags: []string{TagSourceStructure},
			},
		},
	}
	c, err := NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Path-like values that would otherwise invite prefix/shape reconstruction.
	for _, v := range []string{
		"bucket/client-a/dataset/file1.xml",
		"bucket/client-b/dataset/file2.xml",
	} {
		c.ObserveData(map[string]interface{}{"object_key": v})
	}
	fr := c.Snapshot().Fields["object_key"]
	if fr.Shape != ShapeOpaqueString {
		t.Fatalf("shape = %q, want opaque_string", fr.Shape)
	}
	if fr.Distinct != nil {
		t.Fatalf("must not enumerate source_structure values: %#v", *fr.Distinct)
	}
	if blob := mustJSON(t, c.Snapshot()); strings.Contains(blob, "client-a") {
		t.Fatalf("path segment leaked: %s", blob)
	}
}

func TestHighCardinalityCapStopsGrowth(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		MaxDistinct: 3,
		Fields: []FieldConfig{
			{Field: "id", SafeToProfile: true, Sensitivity: SensitivityPublic},
		},
	}
	c, err := NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		c.ObserveData(map[string]interface{}{"id": "v" + string(rune('a'+i))})
	}
	fr := c.Snapshot().Fields["id"]
	if fr.Status != StatusHighCardinalityCapped {
		t.Fatalf("status = %q, want capped", fr.Status)
	}
	if fr.Tier != TierAggregates {
		t.Fatalf("over-cap falls to aggregates, got %q", fr.Tier)
	}
	if got, ok := fr.DistinctCount.(string); !ok || got != ">=3" {
		t.Fatalf("distinct_count = %#v, want >=3", fr.DistinctCount)
	}
	if fr.Distinct != nil {
		t.Fatalf("capped field must drop value map: %#v", *fr.Distinct)
	}
}

func TestSmallCellSuppressionOnQuasiIdentifier(t *testing.T) {
	cfg := Config{
		Enabled:            true,
		MaxDistinct:        20,
		SmallCellThreshold: 3,
		Fields: []FieldConfig{
			{
				Field:          "region",
				SafeToProfile:  true,
				Sensitivity:    SensitivityInternal,
				ProtectionTags: []string{TagQuasiIdentifier},
			},
		},
	}
	c, err := NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// "north" appears 5 times (kept); "south" once (suppressed).
	for i := 0; i < 5; i++ {
		c.ObserveData(map[string]interface{}{"region": "north"})
	}
	c.ObserveData(map[string]interface{}{"region": "south"})
	fr := c.Snapshot().Fields["region"]
	if fr.Tier != TierEnumeration {
		t.Fatalf("tier = %q", fr.Tier)
	}
	if fr.Distinct == nil {
		t.Fatal("enumeration tier must retain distinct object")
	}
	if _, ok := (*fr.Distinct)["south"]; ok {
		t.Fatalf("singleton quasi cell must be suppressed: %#v", *fr.Distinct)
	}
	if (*fr.Distinct)["north"] != 5 {
		t.Fatalf("north = %d, want 5", (*fr.Distinct)["north"])
	}
}

func TestSmallCellDistinctCountOnTierB(t *testing.T) {
	cfg := Config{
		Enabled:            true,
		SmallCellThreshold: 5,
		Fields: []FieldConfig{
			{
				Field:          "linkage",
				ProtectionTags: []string{TagLinkageKey},
			},
		},
	}
	c, err := NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.ObserveData(map[string]interface{}{"linkage": "a"})
	c.ObserveData(map[string]interface{}{"linkage": "b"})
	fr := c.Snapshot().Fields["linkage"]
	if fr.Tier != TierAggregates {
		t.Fatalf("tier = %q", fr.Tier)
	}
	if got, ok := fr.DistinctCount.(string); !ok || got != "<5" {
		t.Fatalf("distinct_count = %#v, want <5", fr.DistinctCount)
	}
}

func TestStagingDiscardExcludesFailedInput(t *testing.T) {
	cfg := Config{
		Enabled: true,
		Fields: []FieldConfig{
			{Field: "status", SafeToProfile: true, Sensitivity: SensitivityPublic},
		},
	}
	c, err := NewCollector(cfg)
	if err != nil || c == nil {
		t.Fatalf("NewCollector: %v", err)
	}
	c.BeginInput()
	c.ObserveData(map[string]interface{}{"status": "keep"})
	c.CommitInput()
	c.BeginInput()
	c.ObserveData(map[string]interface{}{"status": "drop"})
	c.DiscardInput()
	fr := c.Snapshot().Fields["status"]
	if fr.Distinct == nil {
		t.Fatal("expected enumeration distinct map")
	}
	if (*fr.Distinct)["drop"] != 0 {
		t.Fatalf("discarded input leaked: %#v", *fr.Distinct)
	}
	if (*fr.Distinct)["keep"] != 1 {
		t.Fatalf("committed value missing: %#v", *fr.Distinct)
	}
}

func TestRejectUnknownProtectionTag(t *testing.T) {
	_, err := NewCollector(Config{
		Enabled: true,
		Fields: []FieldConfig{
			{Field: "x", ProtectionTags: []string{"not_a_contract_tag"}},
		},
	})
	if err == nil {
		t.Fatal("expected unknown protection_tag error")
	}
}

func TestRejectMaxDistinctAboveHardCap(t *testing.T) {
	_, err := NewCollector(Config{
		Enabled:     true,
		MaxDistinct: HardMaxDistinct + 1,
		Fields:      []FieldConfig{{Field: "x"}},
	})
	if err == nil {
		t.Fatal("expected max_distinct hard-cap error")
	}
}

func TestInactiveConfigReturnsNilCollector(t *testing.T) {
	c, err := NewCollector(Config{Enabled: false, Fields: []FieldConfig{{Field: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Fatal("disabled config must yield nil collector")
	}
	c, err = NewCollector(Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Fatal("enabled with no fields must yield nil collector")
	}
}

func TestObserveRecordsUsesExtractDataEnvelope(t *testing.T) {
	cfg := Config{
		Enabled: true,
		Fields: []FieldConfig{
			{Field: "name", SafeToProfile: true, Sensitivity: SensitivityPublic},
		},
	}
	c, err := NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.ObserveRecords([]map[string]interface{}{
		{"extract": map[string]interface{}{"data": map[string]interface{}{"name": "A"}}},
		{"extract": map[string]interface{}{"data": map[string]interface{}{"name": "B"}}},
		{"_runtime": true}, // ignored
	})
	fr := c.Snapshot().Fields["name"]
	if fr.DistinctCount != 2 {
		t.Fatalf("distinct_count = %#v, want 2", fr.DistinctCount)
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	// local marshal without importing encoding/json in every call site pattern
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
