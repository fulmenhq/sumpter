package commands

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// inspectPeekBytes bounds the non-consuming look at the start of the input.
const inspectPeekBytes = 512

var (
	errInspectJSON    = errors.New("JSON inspection arrives in a later release")
	errInspectNotXML  = errors.New("input does not look like XML; JSON inspection arrives in a later release")
	errInspectGzipped = errors.New("inspect does not read gzip input; decompress it first (for example: gunzip -k <file>)")
)

// checkInspectInput refuses inputs inspect cannot report on. It only ever
// refuses: the format is chosen by --input-format, never by content. The peek
// does not consume the stream; *reader is replaced with the buffered reader.
func checkInspectInput(inputFormat string, reader *io.Reader) error {
	switch strings.ToLower(strings.TrimSpace(inputFormat)) {
	case "", "xml":
	case "json":
		return errInspectJSON
	default:
		return fmt.Errorf("unknown --input-format %q (supported: xml)", inputFormat)
	}
	br, ok := (*reader).(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(*reader)
		*reader = br
	}
	head, _ := br.Peek(inspectPeekBytes)
	return classifyInspectHead(head)
}

// classifyInspectHead decides from the first bytes whether to refuse. Empty
// input and anything that could be XML pass through unchanged.
func classifyInspectHead(head []byte) error {
	if len(head) == 0 {
		return nil
	}
	if len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b {
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
	rest := bytes.TrimPrefix(head, []byte{0xef, 0xbb, 0xbf})
	rest = bytes.TrimLeft(rest, " \t\r\n")
	if len(rest) == 0 || rest[0] == '<' {
		return nil
	}
	return errInspectNotXML
}
