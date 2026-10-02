package streaming

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
)

func encodingScanners() map[string]func(io.Reader) *RecordScanner {
	return map[string]func(io.Reader) *RecordScanner{
		"records":        func(r io.Reader) *RecordScanner { return NewRecordScanner(r, "//Record") },
		"size-only":      func(r io.Reader) *RecordScanner { return NewRecordScannerSizeOnly(r, "//Record") },
		"encoding-aware": func(r io.Reader) *RecordScanner { return NewRecordScannerSizeOnlyWithEncoding(r, "//Record", "ASCII") },
	}
}

func TestRecordScannerASCIIByteRanges(t *testing.T) {
	for _, label := range []string{"ASCII", "ascii", "US-ASCII", "us-Ascii", "UTF-8"} {
		for name, newScanner := range encodingScanners() {
			t.Run(label+"/"+name, func(t *testing.T) {
				fragments := []string{`<Record id="one"><Text>A&amp;B &#xE9;</Text></Record>`, `<Record id="two"/>`}
				source := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"%s\"?>\r\n<Root>\r\n%s\r\n%s\r\n</Root>", label, fragments[0], fragments[1])
				scanner := newScanner(iotest.OneByteReader(strings.NewReader(source)))
				defer func() { _ = scanner.Close() }()
				for i, fragment := range fragments {
					record, err := scanner.Next()
					if err != nil {
						t.Fatal(err)
					}
					start := strings.Index(source, fragment)
					if record.Num != i+1 || record.StartOffset != int64(start) || record.EndOffset != int64(start+len(fragment)) || record.SizeBytes != int64(len(fragment)) {
						t.Fatalf("record %d range = [%d:%d], size=%d; want [%d:%d]", record.Num, record.StartOffset, record.EndOffset, record.SizeBytes, start, start+len(fragment))
					}
					if source[record.StartOffset:record.EndOffset] != fragment {
						t.Fatal("record source bytes changed")
					}
					if name == "records" && i == 0 {
						var parsed struct {
							Text string `xml:"Text"`
						}
						if err := xml.Unmarshal(record.Raw, &parsed); err != nil {
							t.Fatal(err)
						}
						if parsed.Text != "A&B é" {
							t.Fatalf("text = %q", parsed.Text)
						}
					}
				}
				if _, err := scanner.Next(); err != io.EOF {
					t.Fatalf("final error = %v", err)
				}
				if scanner.BytesRead() != int64(len(source)) {
					t.Fatalf("bytes read = %d, want %d", scanner.BytesRead(), len(source))
				}
			})
		}
	}
}

func TestRecordScannerASCIIInvalidBytes(t *testing.T) {
	for _, label := range []string{"ASCII", "us-ascii"} {
		for name, newScanner := range encodingScanners() {
			for _, oneByte := range []bool{false, true} {
				for _, body := range []string{
					`<Root><Record>` + "\x80" + `</Record></Root>`,
					`<Root><Record>é</Record></Root>`,
					`<Root><!--é--><Record/></Root>`,
					`<Root><Record/><!--é--></Root>`,
				} {
					t.Run(fmt.Sprintf("%s/%s/one-byte=%t/%x", label, name, oneByte, body), func(t *testing.T) {
						source := fmt.Sprintf(`<?xml version="1.0" encoding="%s"?>%s`, label, body)
						var reader io.Reader = strings.NewReader(source)
						if oneByte {
							reader = iotest.OneByteReader(reader)
						}
						scanner := newScanner(reader)
						defer func() { _ = scanner.Close() }()
						var err error
						for count := 0; count < 3; count++ {
							if _, err = scanner.Next(); err != nil {
								break
							}
						}
						if err == nil || !strings.Contains(err.Error(), "non-ASCII byte") {
							t.Fatalf("error = %v", err)
						}
						if record, again := scanner.Next(); record != nil || again != err {
							t.Fatalf("scanner resumed after rejection: record=%v err=%v", record, again)
						}
					})
				}
			}
		}
	}
}

func TestRecordScannerASCIIUTF8Control(t *testing.T) {
	for _, declaration := range []string{`<?xml version="1.0" encoding="UTF-8"?>`, ""} {
		for name, newScanner := range encodingScanners() {
			t.Run(declaration+"/"+name, func(t *testing.T) {
				fragment := `<Record>café 日本語</Record>`
				source := declaration + `<Root>` + fragment + `</Root>`
				scanner := newScanner(strings.NewReader(source))
				defer func() { _ = scanner.Close() }()
				record, err := scanner.Next()
				if err != nil {
					t.Fatal(err)
				}
				if source[record.StartOffset:record.EndOffset] != fragment {
					t.Fatal("UTF-8 source range changed")
				}
				if _, err := scanner.Next(); err != io.EOF {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRecordScannerASCIIRefusesTranscoding(t *testing.T) {
	for _, label := range []string{"ISO-8859-1", "windows-1252", "UTF-16", "unknown", "Uſ-AſCII"} {
		for _, sizeOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/size-only=%t", label, sizeOnly), func(t *testing.T) {
				source := fmt.Sprintf(`<?xml version="1.0" encoding="%s"?><Root><Record/></Root>`, label)
				scanner := newRecordScanner(strings.NewReader(source), "//Record", sizeOnly)
				defer func() { _ = scanner.Close() }()
				if record, err := scanner.Next(); record != nil || err == nil || err == io.EOF {
					t.Fatalf("unsupported encoding: record=%v err=%v", record, err)
				}
			})
		}
	}
}

func TestRecordScannerASCIIEntityRefusal(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `<!ENTITY outside "expanded">`)
	}))
	defer server.Close()
	for _, label := range []string{"ASCII", "UTF-8"} {
		for _, body := range []string{
			`<Root><Record>&nbsp;</Record></Root>`,
			fmt.Sprintf(`<!DOCTYPE Root SYSTEM "%s"><Root><Record>&outside;</Record></Root>`, server.URL),
		} {
			for name, newScanner := range encodingScanners() {
				t.Run(label+"/"+name+"/"+body, func(t *testing.T) {
					scanner := newScanner(strings.NewReader(fmt.Sprintf(`<?xml version="1.0" encoding="%s"?>%s`, label, body)))
					defer func() { _ = scanner.Close() }()
					if record, err := scanner.Next(); record != nil || err == nil || !strings.Contains(err.Error(), "invalid character entity") {
						t.Fatalf("entity refusal: record=%v err=%v", record, err)
					}
				})
			}
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("external DTD requested %d times", requests.Load())
	}
}

func TestASCIIReaderRejectsAndStaysFailed(t *testing.T) {
	for _, source := range []string{"\x80invalid", "prefix\xffsuffix"} {
		t.Run(fmt.Sprintf("%x", source), func(t *testing.T) {
			reader := &asciiReader{input: strings.NewReader(source)}
			p := make([]byte, len(source))
			n, err := reader.Read(p)
			want := strings.IndexFunc(source, func(r rune) bool { return r > 0x7f })
			if n != want || err == nil || string(p[:n]) != source[:want] {
				t.Fatalf("n=%d err=%v bytes=%q", n, err, p[:n])
			}
			if n, again := reader.Read(p); n != 0 || again != err {
				t.Fatalf("resumed after rejection: n=%d err=%v", n, again)
			}
		})
	}
}
