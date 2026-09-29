package extract

import (
	"fmt"
	"strings"

	"github.com/fulmenhq/sumpter/internal/docnode"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
)

// Input format tokens accepted in this release.
const (
	FormatXML    = "xml"
	FormatJSON   = "json"
	FormatNDJSON = "ndjson"
)

// jsonNoAttributesHint is the fix carried by every attribute-axis rejection
// under json input.
const jsonNoAttributesHint = "JSON has no attributes: use k instead of @k"

// NormalizeInputFormat validates an input format token. An empty token means
// xml. Reserved and later-release tokens fail with their reason.
func NormalizeInputFormat(token string) (string, error) {
	switch t := strings.ToLower(strings.TrimSpace(token)); t {
	case "", FormatXML:
		return FormatXML, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatNDJSON:
		return FormatNDJSON, nil
	case "protobuf":
		return "", fmt.Errorf("input format %q is reserved, not implemented", token)
	default:
		return "", fmt.Errorf("unknown input format %q (supported: xml, json, ndjson)", token)
	}
}

// ResolveInputFormat returns the effective input format for a run and records
// it on sig.FormatType. In recipe mode the recipe's defaults.input.format is
// authoritative and the signature's format_type must agree with it (an
// omitted format_type means xml). Otherwise the signature's format_type
// decides. File names are never consulted. Under json, expressions that use
// the attribute or namespace axis, prefixed name tests, and namespace maps are
// rejected here, before any input is read.
func ResolveInputFormat(recipeFormat string, recipeMode bool, sig *FileSignature, ext *ExtractRecordMatch, app *ApplicabilityConfig) (string, error) {
	if sig == nil {
		return "", fmt.Errorf("signature config is nil")
	}
	sigFormat, err := NormalizeInputFormat(sig.FormatType)
	if err != nil {
		return "", fmt.Errorf("signature format_type: %w", err)
	}
	effective := sigFormat
	if recipeMode {
		effective, err = NormalizeInputFormat(recipeFormat)
		if err != nil {
			return "", fmt.Errorf("recipe defaults.input.format: %w", err)
		}
		if effective != sigFormat {
			if strings.TrimSpace(sig.FormatType) == "" {
				return "", fmt.Errorf("recipe defaults.input.format is %q but signature format_type defaults to xml: add \"format_type: %s\" to the signature", effective, effective)
			}
			return "", fmt.Errorf("recipe defaults.input.format %q does not match signature format_type %q", effective, sigFormat)
		}
	}
	if effective == FormatJSON || effective == FormatNDJSON {
		if err := checkJSONConfig(sig, ext, app); err != nil {
			return "", err
		}
	}
	if err := checkMatchScope(effective, sig, ext, app); err != nil {
		return "", err
	}
	sig.FormatType = effective
	return effective, nil
}

// Signature match scopes.
const (
	MatchScopeDocument = "document"
	MatchScopeRecord   = "record"
)

// checkMatchScope enforces where each signature scope may be used. ndjson
// has no whole document, so its signature must be record-scoped; xml has no
// record-scoped signature evaluation in this release. A record-scoped
// signature is evaluated against the records one selector picks, so the same
// records are scored on every route.
func checkMatchScope(format string, sig *FileSignature, ext *ExtractRecordMatch, app *ApplicabilityConfig) error {
	scope := strings.ToLower(strings.TrimSpace(sig.MatchScope))
	switch scope {
	case "", MatchScopeDocument, MatchScopeRecord:
	default:
		return fmt.Errorf("signature match_scope %q is not supported (supported: document, record)", sig.MatchScope)
	}
	if format == FormatNDJSON {
		if scope != MatchScopeRecord {
			return fmt.Errorf("signature match_scope: ndjson input has no whole document, so the signature must declare \"match_scope: record\"")
		}
		if app != nil {
			return fmt.Errorf("applicability is not supported for ndjson input: ndjson is always read record by record")
		}
	}
	if scope != MatchScopeRecord {
		return nil
	}
	if format == FormatXML {
		return fmt.Errorf("signature match_scope: record is not supported for xml input in this release; it is supported for json and ndjson input")
	}
	if ext == nil {
		return nil
	}
	if len(ext.MatchSelectors) != 1 {
		return fmt.Errorf("signature match_scope: record needs exactly one extract match selector; found %d", len(ext.MatchSelectors))
	}
	if err := streaming.ValidateRecordSelector(ext.MatchSelectors[0].XPath); err != nil {
		return fmt.Errorf("signature match_scope: record: %w", err)
	}
	return nil
}

// isRecordScope reports whether the signature is evaluated per record.
func isRecordScope(sig *FileSignature) bool {
	return sig != nil && strings.EqualFold(strings.TrimSpace(sig.MatchScope), MatchScopeRecord)
}

// checkJSONConfig enforces the json totality rule over every recipe XPath.
func checkJSONConfig(sig *FileSignature, ext *ExtractRecordMatch, app *ApplicabilityConfig) error {
	if len(sig.Namespaces) > 0 {
		return fmt.Errorf("signature namespaces: json input has no namespaces; remove the namespaces map")
	}
	for _, p := range sig.MatchPatterns {
		if err := checkJSONXPath(fmt.Sprintf("signature selector (pattern %q)", p.PatternID), p.Selector); err != nil {
			return err
		}
	}
	if app != nil && strings.EqualFold(strings.TrimSpace(app.Applicability.Type), "xpath") {
		if err := checkJSONXPath("applicability expression", app.Applicability.Expression); err != nil {
			return err
		}
	}
	if ext == nil {
		return nil
	}
	if len(ext.Namespaces) > 0 {
		return fmt.Errorf("extract namespaces: json input has no namespaces; remove the namespaces map")
	}
	for i, s := range ext.MatchSelectors {
		if err := checkJSONXPath(fmt.Sprintf("match selector %d", i+1), s.XPath); err != nil {
			return err
		}
	}
	return checkJSONMappings("field mapping", ext.FieldMappings)
}

func checkJSONMappings(scope string, mappings []FieldMapping) error {
	for i := range mappings {
		m := &mappings[i]
		label := fmt.Sprintf("%s %q", scope, m.OutputField)
		if err := checkJSONXPath(label, m.XPath); err != nil {
			return err
		}
		if err := checkJSONMappings("item mapping", m.ItemMapping); err != nil {
			return err
		}
		for j := range m.Polymorphic {
			pm := &m.Polymorphic[j]
			if err := checkJSONXPath(fmt.Sprintf("%s polymorphic match_xpath", label), pm.MatchXPath); err != nil {
				return err
			}
			if err := checkJSONMappings("polymorphic mapping", pm.FieldMappings); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkJSONXPath(label, expr string) error {
	violations := xpathAxisViolations(expr)
	if len(violations) == 0 {
		return nil
	}
	v := violations[0]
	switch v.Kind {
	case axisAttribute:
		return fmt.Errorf("%s xpath %q uses the attribute axis (%q): %s", label, expr, v.Text, jsonNoAttributesHint)
	case axisNamespace:
		return fmt.Errorf("%s xpath %q uses the namespace axis (%q): json input has no namespaces", label, expr, v.Text)
	default:
		return fmt.Errorf("%s xpath %q uses the prefixed name %q: json input has no namespaces", label, expr, v.Text)
	}
}

type axisKind int

const (
	axisAttribute axisKind = iota
	axisNamespace
	axisPrefixedName
)

// axisViolation is one construct json input cannot satisfy. Start and End are
// byte offsets into the expression, so a later rewrite can work in place.
type axisViolation struct {
	Kind       axisKind
	Start, End int
	Text       string
}

// xpathAxisViolations lexes an XPath 1.0 expression outside string literals
// and reports attribute-axis steps, namespace-axis steps, and prefixed name
// tests. It over-reports rather than under-reports: a false positive is a
// loud, fixable load error.
func xpathAxisViolations(expr string) []axisViolation {
	var out []axisViolation
	n := len(expr)
	for i := 0; i < n; {
		c := expr[i]
		switch {
		case c == '"' || c == '\'':
			end := strings.IndexByte(expr[i+1:], c)
			if end < 0 {
				return out
			}
			i += end + 2
		case c == '@':
			end := i + 1
			for end < n && (isNameChar(expr[end]) || expr[end] == ':' || expr[end] == '*') {
				end++
			}
			out = append(out, axisViolation{Kind: axisAttribute, Start: i, End: end, Text: expr[i:end]})
			i = end
		case isNameStart(c):
			start := i
			for i < n && isNameChar(expr[i]) {
				i++
			}
			name := expr[start:i]
			if i+1 < n && expr[i] == ':' && expr[i+1] == ':' {
				switch name {
				case "attribute":
					out = append(out, axisViolation{Kind: axisAttribute, Start: start, End: i + 2, Text: expr[start : i+2]})
				case "namespace":
					out = append(out, axisViolation{Kind: axisNamespace, Start: start, End: i + 2, Text: expr[start : i+2]})
				}
				i += 2
				continue
			}
			if i+1 < n && expr[i] == ':' && (isNameStart(expr[i+1]) || expr[i+1] == '*') {
				end := i + 1
				for end < n && (isNameChar(expr[end]) || expr[end] == '*') {
					end++
				}
				out = append(out, axisViolation{Kind: axisPrefixedName, Start: start, End: end, Text: expr[start:end]})
				i = end
			}
		default:
			i++
		}
	}
	return out
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isNameChar(c byte) bool {
	return isNameStart(c) || c == '-' || c == '.' || (c >= '0' && c <= '9')
}

// inputFormat returns the registered format recorded on the signature by
// ResolveInputFormat; an empty format_type means xml.
func inputFormat(sig *FileSignature) (docnode.Format, error) {
	token := docnode.Default
	if sig != nil && strings.TrimSpace(sig.FormatType) != "" {
		normalized, err := NormalizeInputFormat(sig.FormatType)
		if err != nil {
			return nil, err
		}
		token = normalized
	}
	format, ok := docnode.Lookup(token)
	if !ok {
		return nil, fmt.Errorf("input format %q is not registered", token)
	}
	return format, nil
}

// jsonStreamingBlocked is the refusal for a json input above the large-file
// threshold that cannot take the streaming route. It names what blocks the
// route and the whole-document opt-in.
func jsonStreamingBlocked(blockers []string) error {
	return fmt.Errorf("json input above the large-file threshold cannot be streamed because %s; use --allow-large-files to parse it as one document: %w", strings.Join(blockers, " and "), docnode.ErrRouteUnsupported)
}

// jsonStreamingBlockers lists what keeps a json input off the streaming
// route: the streaming route has no whole document to evaluate a
// document-scoped signature or an applicability predicate against, and it
// writes only through a record sink.
func jsonStreamingBlockers(sig *FileSignature, app *ApplicabilityConfig, sink RecordSink) []string {
	var blockers []string
	if !isRecordScope(sig) {
		blockers = append(blockers, "the signature is evaluated against the whole document (match_scope: document)")
	}
	if app != nil {
		blockers = append(blockers, "the recipe declares an applicability predicate")
	}
	if sink == nil {
		blockers = append(blockers, "the output is buffered")
	}
	return blockers
}

func isJSONInput(sig *FileSignature) bool {
	return sig != nil && strings.EqualFold(strings.TrimSpace(sig.FormatType), FormatJSON)
}

func isNDJSONInput(sig *FileSignature) bool {
	return sig != nil && strings.EqualFold(strings.TrimSpace(sig.FormatType), FormatNDJSON)
}

// checkUnmatchedPolymorphic fails a json polymorphic mapping by element_type
// that matched nothing while items exist: bare array items all carry their
// parent key's name, so the wrapper idiom is required. XML keeps its empty
// result.
func checkUnmatchedPolymorphic(format docnode.Format, sources []docnode.Node, mapping *FieldMapping) error {
	if format == nil || (format.Token() != FormatJSON && format.Token() != FormatNDJSON) {
		return nil
	}
	byElementType := false
	for i := range mapping.Polymorphic {
		if strings.TrimSpace(mapping.Polymorphic[i].ElementType) != "" {
			byElementType = true
			break
		}
	}
	if !byElementType {
		return nil
	}
	items := 0
	for _, src := range sources {
		if _, ok := src.FirstElementChild(); ok {
			items++
		}
	}
	if items == 0 {
		return nil
	}
	return fmt.Errorf("field mapping %q: no polymorphic branch matched %d items; expected the wrapper idiom, where each array item is an object keyed by its element type (for example [{\"OrderLine\": {...}}])", mapping.OutputField, items)
}

// recordSignatureMismatch is the error for the first record a record-scoped
// signature does not admit. It names the record by ordinal only.
func recordSignatureMismatch(num int, confidence, threshold float64) error {
	return fmt.Errorf("signature mismatch: record %d scored confidence=%.3f below threshold=%.3f; with match_scope: record the signature is evaluated per record", num, confidence, threshold)
}

// matchesSignaturePerRecord scores a record-scoped signature on the
// whole-document route exactly as the streaming route does: each element the
// single match selector picks, in document order, is copied into its own
// record document and scored; the first record below the threshold fails the
// input. With no records nothing is scored and the status stays unknown. The
// confidence is the lowest record score.
func matchesSignaturePerRecord(doc docnode.Document, sig *FileSignature, ext *ExtractRecordMatch) (SignatureMatchStatus, float64, error) {
	if ext == nil || len(ext.MatchSelectors) != 1 {
		return SignatureMatchUnknown, 0, fmt.Errorf("signature match_scope: record needs exactly one extract match selector")
	}
	format := doc.Format()
	documenter, ok := format.(docnode.RecordDocumenter)
	if !ok {
		return SignatureMatchUnknown, 0, fmt.Errorf("signature match_scope: record is not supported for %s input", format.Token())
	}
	nodes, err := evaluateNodeSet(format, doc.Root(), nil, ext.MatchSelectors[0].XPath)
	if err != nil {
		return SignatureMatchUnknown, 0, fmt.Errorf("failed to evaluate XPath %s: %w", ext.MatchSelectors[0].XPath, err)
	}
	minConfidence := 0.0
	for i, n := range nodes {
		recordDoc, err := documenter.RecordDocument(n)
		if err != nil {
			return SignatureMatchUnknown, 0, err
		}
		matches, confidence, err := matchesSignature(recordDoc.Root(), sig)
		if err != nil {
			return SignatureMatchUnknown, 0, fmt.Errorf("failed to check signature: %w", err)
		}
		if i == 0 || confidence < minConfidence {
			minConfidence = confidence
		}
		if !matches {
			return SignatureMatchMismatched, minConfidence, recordSignatureMismatch(i+1, confidence, sig.ConfidenceThreshold)
		}
	}
	if len(nodes) == 0 {
		return SignatureMatchUnknown, 0, nil
	}
	return SignatureMatchMatched, minConfidence, nil
}

// IsRecordScope reports whether a signature is evaluated per record.
func IsRecordScope(sig *FileSignature) bool { return isRecordScope(sig) }

// ScoreRecordSignature scores a record-scoped signature against one record's
// document, as the streaming route does.
func ScoreRecordSignature(doc docnode.Document, sig *FileSignature) (bool, float64, error) {
	return matchesSignature(doc.Root(), sig)
}

// RecordSignatureMismatch is the error for the first record a record-scoped
// signature does not admit.
func RecordSignatureMismatch(num int, confidence, threshold float64) error {
	return recordSignatureMismatch(num, confidence, threshold)
}

// CloneRecordMatchForRecordDocument returns a copy of cfg whose match
// selector picks the root element of a record document, as the streaming
// route extracts each scanned record.
func CloneRecordMatchForRecordDocument(cfg *ExtractRecordMatch) *ExtractRecordMatch {
	return cloneExtractConfigForStreaming(cfg)
}

// ExtractRecordsFromDocument extracts the records cfg selects from doc; it
// returns none when the selection is filtered out.
func ExtractRecordsFromDocument(doc docnode.Document, cfg *ExtractRecordMatch, externalFields map[string]interface{}) ([]map[string]interface{}, error) {
	return extractRecords(doc, cfg, externalFields)
}
