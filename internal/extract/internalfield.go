package extract

// InternalField wraps an external-field value that must be visible in
// field_mappings[].expression scope but never emitted into the record body
// (extract.data) on any sink. It is the carrier for a source_extraction pattern
// declared `internal: true`: the value participates in expression evaluation and
// in required/collision checks, but the record-emission merge skips it.
//
// Encoding internal-ness in the value (rather than threading a separate name set
// through every extraction entrypoint) keeps the external-field map signature
// — map[string]interface{} — unchanged across the DOM, streaming, indexed
// parallel, and extract-multi parsed-document paths. Only two call sites need to
// know about it: expression-scope construction unwraps it, and record emission
// skips it.
type InternalField struct {
	Value interface{}
}

// unwrapExternalFieldValue returns the underlying value of an external field,
// unwrapping an InternalField so expression scope sees the real captured value.
func unwrapExternalFieldValue(value interface{}) interface{} {
	if internal, ok := value.(InternalField); ok {
		return internal.Value
	}
	return value
}

// isInternalField reports whether an external field is derive-only (never
// emitted into the record body).
func isInternalField(value interface{}) bool {
	_, ok := value.(InternalField)
	return ok
}
