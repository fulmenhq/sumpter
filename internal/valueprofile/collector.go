package valueprofile

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Profile is the guarded diagnostic object written to the provenance manifest.
type Profile struct {
	Version            string                 `json:"version"`
	MaxDistinct        int                    `json:"max_distinct"`
	SmallCellThreshold int                    `json:"small_cell_threshold"`
	Fields             map[string]FieldResult `json:"fields"`
}

// FieldResult is one field's Tier-A or Tier-B emission.
// Distinct is a pointer so Tier A can emit an empty object after small-cell
// suppression (required by the provenance schema) while Tier B omits the key.
type FieldResult struct {
	Tier          string          `json:"tier"`
	Status        string          `json:"status"`
	Count         int             `json:"count"`
	NullCount     int             `json:"null_count"`
	DistinctCount interface{}     `json:"distinct_count"`
	Distinct      *map[string]int `json:"distinct,omitempty"`
	Shape         string          `json:"shape,omitempty"`
	Length        *LengthStats    `json:"length,omitempty"`
	// Numeric range is Tier-A only (sensitive numerics never emit min/max).
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// LengthStats holds non-reconstructive string length aggregates.
type LengthStats struct {
	Min  int     `json:"min"`
	Max  int     `json:"max"`
	Mean float64 `json:"mean"`
}

// Collector accumulates streaming per-field observations under the guard.
//
// Transactional inputs use BeginInput/CommitInput/DiscardInput so failed or
// floor-rejected records never enter the shareable profile. Direct
// ObserveRecords (no open input) commits immediately — only for paths that
// already know the records are durable.
type Collector struct {
	cfg       Config
	mu        sync.Mutex
	committed map[string]*fieldState
	// staged is non-nil while an input transaction is open.
	staged map[string]*fieldState
}

type fieldState struct {
	cfg         FieldConfig
	count       int
	nullCount   int
	distinct    map[string]int
	capped      bool
	lenMin      int
	lenMax      int
	lenSum      int64
	lenN        int
	shape       shapeAccumulator
	numMin      float64
	numMax      float64
	hasNumeric  bool
	allNumeric  bool
	sawNonEmpty bool
}

type shapeAccumulator struct {
	uuid  bool
	email bool
	free  bool
	empty bool
	init  bool
}

var (
	uuidRE  = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// NewCollector builds a collector for an active config. Returns nil when the
// config is inactive (caller should skip observe/snapshot).
func NewCollector(cfg Config) (*Collector, error) {
	normalized, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	if !normalized.Active() {
		return nil, nil
	}
	return &Collector{
		cfg:       normalized,
		committed: newFieldStates(normalized.Fields),
	}, nil
}

func newFieldStates(fields []FieldConfig) map[string]*fieldState {
	out := make(map[string]*fieldState, len(fields))
	for _, f := range fields {
		out[f.Field] = &fieldState{
			cfg:        f,
			distinct:   make(map[string]int),
			lenMin:     math.MaxInt,
			allNumeric: true,
		}
	}
	return out
}

// BeginInput opens a staging transaction for the current input. Subsequent
// observations go to the stage until CommitInput or DiscardInput.
func (c *Collector) BeginInput() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.staged = newFieldStates(c.cfg.Fields)
}

// BeginInputIfNeeded opens a stage only when none is open (safe for paths that
// stage records before an explicit Begin).
func (c *Collector) BeginInputIfNeeded() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.staged == nil {
		c.staged = newFieldStates(c.cfg.Fields)
	}
}

// CommitInput merges the staged observations into the committed profile.
func (c *Collector) CommitInput() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.staged == nil {
		return
	}
	for name, pending := range c.staged {
		committed := c.committed[name]
		if committed == nil {
			continue
		}
		committed.mergeFrom(pending, c.cfg.MaxDistinct)
	}
	c.staged = nil
}

// DiscardInput drops staged observations so failed/floor-rejected inputs never
// influence the shareable profile.
func (c *Collector) DiscardInput() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.staged = nil
}

// ObserveRecords profiles extract.data from complete extract record envelopes.
// When an input transaction is open, observations are staged; otherwise they
// commit immediately (for already-durable record sets only).
func (c *Collector) ObserveRecords(records []map[string]interface{}) {
	if c == nil {
		return
	}
	for _, record := range records {
		data := extractDataMap(record)
		if data == nil {
			continue
		}
		c.ObserveData(data)
	}
}

// ObserveData profiles one extract.data object.
func (c *Collector) ObserveData(data map[string]interface{}) {
	if c == nil || data == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.committed
	if c.staged != nil {
		target = c.staged
	}
	observeDataInto(target, data, c.cfg.MaxDistinct)
}

func observeDataInto(fields map[string]*fieldState, data map[string]interface{}, maxDistinct int) {
	for name, state := range fields {
		raw, ok := data[name]
		state.count++
		if !ok || raw == nil {
			state.nullCount++
			continue
		}
		text := stringifyValue(raw)
		if text == "" && isEmptyScalar(raw) {
			state.nullCount++
			continue
		}
		state.observeValue(text, raw, maxDistinct)
	}
}

// mergeFrom folds a staged field state into the committed state.
func (s *fieldState) mergeFrom(other *fieldState, maxDistinct int) {
	if other == nil {
		return
	}
	s.count += other.count
	s.nullCount += other.nullCount
	if other.sawNonEmpty {
		s.sawNonEmpty = true
	}
	if other.lenN > 0 {
		if s.lenN == 0 || other.lenMin < s.lenMin {
			s.lenMin = other.lenMin
		}
		if other.lenMax > s.lenMax {
			s.lenMax = other.lenMax
		}
		s.lenSum += other.lenSum
		s.lenN += other.lenN
	}
	if other.hasNumeric {
		if !s.hasNumeric {
			s.numMin, s.numMax = other.numMin, other.numMax
			s.hasNumeric = true
		} else {
			if other.numMin < s.numMin {
				s.numMin = other.numMin
			}
			if other.numMax > s.numMax {
				s.numMax = other.numMax
			}
		}
	}
	if !other.allNumeric {
		s.allNumeric = false
	}
	s.shape.merge(other.shape)

	if s.capped {
		return
	}
	if other.capped {
		s.capped = true
		s.distinct = nil
		return
	}
	if other.distinct == nil {
		return
	}
	if s.distinct == nil {
		s.distinct = make(map[string]int)
	}
	for value, n := range other.distinct {
		if s.capped {
			return
		}
		s.distinct[value] += n
		if len(s.distinct) > maxDistinct {
			s.capped = true
			s.distinct = nil
			return
		}
	}
}

func (s *fieldState) observeValue(text string, raw interface{}, maxDistinct int) {
	s.sawNonEmpty = true
	// Length stats (string form).
	n := len(text)
	if n < s.lenMin {
		s.lenMin = n
	}
	if n > s.lenMax {
		s.lenMax = n
	}
	s.lenSum += int64(n)
	s.lenN++

	// Numeric tracking for measure range (Tier-A only later).
	if f, ok := toFloat(raw); ok {
		if !s.hasNumeric {
			s.numMin, s.numMax = f, f
			s.hasNumeric = true
		} else {
			if f < s.numMin {
				s.numMin = f
			}
			if f > s.numMax {
				s.numMax = f
			}
		}
	} else {
		s.allNumeric = false
	}

	s.shape.observe(text)

	if s.capped {
		return
	}
	if _, exists := s.distinct[text]; exists {
		s.distinct[text]++
		return
	}
	if len(s.distinct) >= maxDistinct {
		s.capped = true
		// Drop the value map so memory cannot grow unbounded after the cap.
		s.distinct = nil
		return
	}
	s.distinct[text] = 1
}

// Snapshot applies the default-deny guard and returns the shareable profile.
// Gate is re-derived from config + observed aggregates — never a self-asserted
// "suppression applied" flag.
func (c *Collector) Snapshot() *Profile {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Snapshot only committed state — open staged inputs are excluded until
	// CommitInput (and are dropped by DiscardInput on failure).
	out := &Profile{
		Version:            ProfileVersion,
		MaxDistinct:        c.cfg.MaxDistinct,
		SmallCellThreshold: c.cfg.SmallCellThreshold,
		Fields:             make(map[string]FieldResult, len(c.committed)),
	}
	names := make([]string, 0, len(c.committed))
	for name := range c.committed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.Fields[name] = c.committed[name].result(c.cfg)
	}
	return out
}

func (s *fieldState) result(cfg Config) FieldResult {
	status := StatusComplete
	if s.capped {
		status = StatusHighCardinalityCapped
	}

	// Tier A: safe_to_profile + public|internal + under cap + still holding values.
	if s.allowsTierA() && !s.capped && s.distinct != nil {
		distinct := copyDistinct(s.distinct)
		// Small-cell suppression for quasi/linkage even under Tier A.
		if s.hasTag(TagQuasiIdentifier) || s.hasTag(TagLinkageKey) {
			distinct = suppressSmallCells(distinct, cfg.SmallCellThreshold)
		}
		if distinct == nil {
			distinct = map[string]int{}
		}
		res := FieldResult{
			Tier:          TierEnumeration,
			Status:        status,
			Count:         s.count,
			NullCount:     s.nullCount,
			DistinctCount: len(s.distinct),
			Distinct:      &distinct,
		}
		// Numeric range only under Tier A (and only when all observed values numeric).
		if s.hasNumeric && s.allNumeric {
			minV, maxV := s.numMin, s.numMax
			res.Min = &minV
			res.Max = &maxV
		}
		return res
	}

	// Tier B — aggregates only.
	res := FieldResult{
		Tier:      TierAggregates,
		Status:    status,
		Count:     s.count,
		NullCount: s.nullCount,
		Shape:     s.shapeClass(),
	}
	if s.capped {
		res.DistinctCount = fmt.Sprintf(">=%d", cfg.MaxDistinct)
	} else if s.distinct != nil {
		dc := len(s.distinct)
		if (s.hasTag(TagQuasiIdentifier) || s.hasTag(TagLinkageKey)) && dc > 0 && dc < cfg.SmallCellThreshold {
			res.DistinctCount = fmt.Sprintf("<%d", cfg.SmallCellThreshold)
		} else {
			res.DistinctCount = dc
		}
	} else {
		res.DistinctCount = 0
	}
	if s.lenN > 0 {
		res.Length = &LengthStats{
			Min:  s.lenMin,
			Max:  s.lenMax,
			Mean: float64(s.lenSum) / float64(s.lenN),
		}
	}
	// Never emit numeric min/max under Tier B (sensitive numerics).
	return res
}

func (s *fieldState) allowsTierA() bool {
	if !s.cfg.SafeToProfile {
		return false
	}
	switch s.cfg.Sensitivity {
	case SensitivityPublic, SensitivityInternal:
		return true
	default:
		return false
	}
}

func (s *fieldState) hasTag(tag string) bool {
	for _, t := range s.cfg.ProtectionTags {
		if t == tag {
			return true
		}
	}
	return false
}

func (s *fieldState) shapeClass() string {
	// source_structure / direct_identifier MUST collapse to opaque_string.
	if s.hasTag(TagSourceStructure) || s.hasTag(TagDirectIdentifier) {
		return ShapeOpaqueString
	}
	if !s.sawNonEmpty {
		return ShapeFreeform
	}
	if s.allNumeric && s.hasNumeric {
		return ShapeAllNumeric
	}
	return s.shape.class()
}

func (a *shapeAccumulator) observe(text string) {
	if text == "" {
		a.empty = true
		return
	}
	if !a.init {
		a.init = true
		a.uuid = uuidRE.MatchString(text)
		a.email = emailRE.MatchString(text)
		a.free = !a.uuid && !a.email
		return
	}
	if a.uuid && !uuidRE.MatchString(text) {
		a.uuid = false
	}
	if a.email && !emailRE.MatchString(text) {
		a.email = false
	}
	if !a.uuid && !a.email {
		a.free = true
	}
}

func (a *shapeAccumulator) merge(other shapeAccumulator) {
	if !other.init && !other.empty {
		return
	}
	if !a.init {
		*a = other
		return
	}
	if other.empty {
		a.empty = true
	}
	if a.uuid && !other.uuid {
		a.uuid = false
	}
	if a.email && !other.email {
		a.email = false
	}
	if !a.uuid && !a.email {
		a.free = true
	}
}

func (a *shapeAccumulator) class() string {
	if a.uuid {
		return ShapeUUIDShaped
	}
	if a.email {
		return ShapeEmailShaped
	}
	return ShapeFreeform
}

func suppressSmallCells(in map[string]int, threshold int) map[string]int {
	if threshold <= 1 {
		return in
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		if v >= threshold {
			out[k] = v
		}
	}
	return out
}

func copyDistinct(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func extractDataMap(record map[string]interface{}) map[string]interface{} {
	if record == nil {
		return nil
	}
	if data, ok := record["extract"].(map[string]interface{}); ok {
		if inner, ok := data["data"].(map[string]interface{}); ok {
			return inner
		}
	}
	// Also accept a bare data map for unit tests.
	if _, hasExtract := record["extract"]; !hasExtract {
		return record
	}
	return nil
}

func stringifyValue(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == math.Trunc(t) && t >= math.MinInt64 && t <= math.MaxInt64 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case float32:
		return stringifyValue(float64(t))
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	default:
		return fmt.Sprint(v)
	}
}

func isEmptyScalar(v interface{}) bool {
	if s, ok := v.(string); ok {
		return s == ""
	}
	return false
}

func toFloat(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case int32:
		return float64(t), true
	case uint64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	default:
		// Reject non-numeric shapes that fmt would stringify.
		rv := fmt.Sprintf("%T", v)
		if strings.Contains(rv, "int") || strings.Contains(rv, "float") {
			return 0, false
		}
		_ = unicode.ReplacementChar
		return 0, false
	}
}
