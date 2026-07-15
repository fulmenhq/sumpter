package extract

// InternalField wraps a derive-only value that must be visible in
// field_mappings[].expression scope but never emitted into the record body
// (extract.data) on any sink. Carriers include:
//   - source_extraction patterns declared `internal: true`
//   - parameters_internal / --parameter-internal
//   - top-level field_mappings declared `internal: true`
//
// The value participates in expression evaluation and collision checks, then
// is projected out before filters, uniform-schema fill, output validation,
// enrichment, value_profile, and sinks.
//
// Encoding internal-ness in the value (rather than threading a separate name set
// through every extraction entrypoint) keeps the external-field map signature
// — map[string]interface{} — unchanged across the DOM, streaming, indexed
// parallel, and extract-multi parsed-document paths. Expression-scope
// construction unwraps it from both the record map and external fields;
// projection removes mapped InternalField keys before any post-mapping stage.
type InternalField struct {
	Value interface{}
}

// unwrapInternalFieldValue returns the underlying value of a possibly-wrapped
// InternalField so expression scope sees the real value (record-mapped or
// external carriers).
func unwrapInternalFieldValue(value interface{}) interface{} {
	if internal, ok := value.(InternalField); ok {
		return internal.Value
	}
	return value
}

// isInternalField reports whether a value is derive-only (never emitted into
// the record body).
func isInternalField(value interface{}) bool {
	_, ok := value.(InternalField)
	return ok
}

// storeMappedValue places a field-mapping result on the record. Non-nil
// internal mappings are wrapped so later expressions can unwrap them and
// projection can strip them before emit.
func storeMappedValue(record map[string]interface{}, mapping *FieldMapping, value interface{}) {
	if record == nil || mapping == nil || value == nil {
		return
	}
	if mapping.Internal {
		record[mapping.OutputField] = InternalField{Value: value}
		return
	}
	record[mapping.OutputField] = value
}

// projectInternalMappedFields removes InternalField-wrapped keys from the
// record after expression evaluation and external-field collision checks.
// External internals are never merged into the record; this clears mapped
// internals only (or any accidental wrapper that reached the map).
func projectInternalMappedFields(record map[string]interface{}) {
	if record == nil {
		return
	}
	for key, value := range record {
		if isInternalField(value) {
			delete(record, key)
		}
	}
}
