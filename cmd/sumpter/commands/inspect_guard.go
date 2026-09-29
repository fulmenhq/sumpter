package commands

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

// inspectPeekBytes bounds the non-consuming look at the start of the input.
const inspectPeekBytes = 512

var (
	errInspectNotXML    = errors.New("input does not look like XML; use --input-format json for JSON input")
	errInspectLooksXML  = errors.New("input looks like XML; drop --input-format json")
	errInspectGzipped   = errors.New("inspect does not read gzip input; decompress it first (for example: gunzip -k <file>)")
	errInspectJSONForce = errors.New("--force-encoding does not apply to --input-format json; JSON input must be UTF-8")
)

// resolveInspectInputFormat validates --input-format and the flags that do
// not combine with it. It runs before any input is opened or read.
func resolveInspectInputFormat(opts *InspectOptions) (string, error) {
	switch token := strings.ToLower(strings.TrimSpace(opts.InputFormat)); token {
	case "", inspectFormatXML:
		return inspectFormatXML, nil
	case inspectFormatJSON:
	case inspectFormatNDJSON:
		if opts.AnalyzeRecords {
			return "", errInspectAnalyzeRecords(token)
		}
		return "", fmt.Errorf("inspect is not supported for ndjson input in this release: %w", docnode.ErrRouteUnsupported)
	default:
		return "", fmt.Errorf("unknown --input-format %q (supported: xml, json, ndjson)", opts.InputFormat)
	}
	if opts.ForceEncoding != "" {
		return "", errInspectJSONForce
	}
	if opts.AnalyzeRecords {
		return "", errInspectAnalyzeRecords(inspectFormatJSON)
	}
	return inspectFormatJSON, nil
}

// errInspectAnalyzeRecords refuses record analysis for a JSON input format.
func errInspectAnalyzeRecords(token string) error {
	return fmt.Errorf("record analysis is not supported for %s input in this release: %w", token, docnode.ErrRouteUnsupported)
}

// checkInspectInput refuses inputs inspect cannot report on under the
// declared format. It only ever refuses: the format is chosen by
// --input-format, never by content. The peek does not consume the stream;
// *reader is replaced with the buffered reader.
func checkInspectInput(inputFormat string, reader *io.Reader) error {
	br, ok := (*reader).(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(*reader)
		*reader = br
	}
	head, _ := br.Peek(inspectPeekBytes)
	if inputFormat == inspectFormatJSON {
		return classifyInspectJSONHead(head)
	}
	return classifyInspectHead(head)
}

// classifyInspectHead decides from the first bytes whether to refuse XML
// inspection. Empty input and anything that could be XML pass through
// unchanged.
func classifyInspectHead(head []byte) error {
	if len(head) == 0 {
		return nil
	}
	if isGzipMagic(head) {
		return errInspectGzipped
	}
	// UTF-16 with a BOM, or BOM-less UTF-16 starting with '<': let the
	// existing decoder handle it exactly as before.
	if len(head) >= 2 && ((head[0] == 0xff && head[1] == 0xfe) || (head[0] == 0xfe && head[1] == 0xff)) {
		return nil
	}
	if len(head) >= 2 && ((head[0] == '<' && head[1] == 0) || (head[0] == 0 && head[1] == '<')) {
		return nil
	}
	rest := firstNonSpace(head)
	if len(rest) == 0 || rest[0] == '<' {
		return nil
	}
	return errInspectNotXML
}

// classifyInspectJSONHead decides from the first bytes whether to refuse JSON
// inspection. Everything else, including empty input, is left to the JSON
// walker and its errors.
func classifyInspectJSONHead(head []byte) error {
	if isGzipMagic(head) {
		return errInspectGzipped
	}
	if rest := firstNonSpace(head); len(rest) > 0 && rest[0] == '<' {
		return errInspectLooksXML
	}
	return nil
}

func isGzipMagic(head []byte) bool {
	return len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b
}

// firstNonSpace skips an optional UTF-8 BOM and leading whitespace.
func firstNonSpace(head []byte) []byte {
	rest := bytes.TrimPrefix(head, []byte{0xef, 0xbb, 0xbf})
	return bytes.TrimLeft(rest, " \t\r\n")
}
