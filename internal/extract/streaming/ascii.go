package streaming

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

func isASCIIEncoding(label string) bool {
	return strings.EqualFold(label, "ASCII") || strings.EqualFold(label, "US-ASCII")
}

// asciiCharsetReader accepts only byte-preserving ASCII declarations. General
// transcoding would make decoder offsets unsuitable for original-byte indexes.
// UTF-8 is handled directly by encoding/xml, without invoking this callback.
func asciiCharsetReader(label string, input io.Reader) (io.Reader, error) {
	if !isASCIIEncoding(label) {
		return nil, fmt.Errorf("XML encoding %q is not supported for byte-preserving scanning", label)
	}
	return &asciiReader{input: input}, nil
}

type asciiReader struct {
	input io.Reader
	err   error
}

func (r *asciiReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.input.Read(p)
	for i, b := range p[:n] {
		if b > 0x7f {
			r.err = errors.New("non-ASCII byte in ASCII-declared XML")
			return i, r.err
		}
	}
	return n, err
}
