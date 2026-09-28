package json

import (
	"fmt"
	"strings"
	"testing"
	"testing/iotest"
)

// walkParityCases are inputs whose parse outcome the streaming walk must
// reproduce exactly, error text included.
func walkParityCases() []string {
	return []string{
		``, `   `, "\n\t", `42`, `"x"`, `true`, `null`,
		`{`, `[`, `{"a":`, `{"a":[1,2`, `{"a":1`, `[1,2`,
		`{"a":1,"a":2}`, `{"b":0,"a":1,"a":2}`, `{"a\"b":1,"a\"b":2}`, `{"x":{"k":1,"k":2}}`,
		"{\"s\":\"ab\xffcd\"}", "\xff{}", "{\"a\":1}\xff",
		`{"a":1} {"b":2}`, `{"a":1} x`, `[1] [2]`, `{"a":1}   `, "{\"a\":1}\n",
		`{"a":tru}`, `{"a":1,}`, `{,}`, `{"a" 1}`, `[1,,2]`, `{"a":01}`, `{1:2}`,
		strings.Repeat("[", MaxDepth+1) + strings.Repeat("]", MaxDepth+1),
		strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth),
		`{"a":{"b":[1,"x",true,null,{"c":[]},[2,[3]]]},"é":"ü"}`,
	}
}

type recorder struct{ events []string }

func (r *recorder) StartElement(name string, kind Kind) error {
	r.events = append(r.events, fmt.Sprintf("<%s:%d>", name, kind))
	return nil
}

func (r *recorder) Text(value string) error {
	r.events = append(r.events, fmt.Sprintf("%q", value))
	return nil
}

func (r *recorder) EndElement() error {
	r.events = append(r.events, "</>")
	return nil
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func TestWalkMatchesParse(t *testing.T) {
	for i, src := range walkParityCases() {
		_, parseErr := parse([]byte(src))
		for _, mode := range []string{"whole", "one-byte"} {
			var r = strings.NewReader(src)
			var walkErr error
			if mode == "whole" {
				walkErr = Walk(r, &recorder{})
			} else {
				walkErr = Walk(iotest.OneByteReader(r), &recorder{})
			}
			if errText(walkErr) != errText(parseErr) {
				t.Errorf("case %d (%s): Walk error %q, Parse error %q", i, mode, errText(walkErr), errText(parseErr))
			}
		}
	}
}

func TestWalkEvents(t *testing.T) {
	var r recorder
	if err := Walk(strings.NewReader(`{"a":{"b":[1,[2,3],null],"e":[]},"s":"x"}`), &r); err != nil {
		t.Fatal(err)
	}
	want := `<a:0> <b:3> "1" </> <b:1> <b:3> "2" </> <b:3> "3" </> </> <b:5> </> </> <s:2> "x" </>`
	if got := strings.Join(r.events, " "); got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
}

func TestWalkTopLevelArrayItems(t *testing.T) {
	var r recorder
	if err := Walk(strings.NewReader(`[{"id":"a"},"b"]`), &r); err != nil {
		t.Fatal(err)
	}
	want := `<item:0> <id:2> "a" </> </> <item:2> "b" </>`
	if got := strings.Join(r.events, " "); got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
}

func TestValidatingReaderSplitRunes(t *testing.T) {
	src := `{"k":"é中😀"}`
	if err := Walk(iotest.OneByteReader(strings.NewReader(src)), &recorder{}); err != nil {
		t.Fatalf("multi-byte runes split across reads rejected: %v", err)
	}
	if err := Walk(iotest.OneByteReader(strings.NewReader("{\"k\":\"\xe4\xb8\"}")), &recorder{}); errText(err) != "json: invalid UTF-8 at byte offset 6" {
		t.Fatalf("truncated rune: %v", err)
	}
}

func TestWalkHandlerErrorStops(t *testing.T) {
	stop := fmt.Errorf("stop")
	h := &stopAfter{n: 2, err: stop}
	if err := Walk(strings.NewReader(`{"a":1,"b":2,"c":3}`), h); err != stop {
		t.Fatalf("error = %v, want handler error", err)
	}
}

type stopAfter struct {
	n   int
	err error
}

func (s *stopAfter) StartElement(string, Kind) error {
	s.n--
	if s.n < 0 {
		return s.err
	}
	return nil
}
func (s *stopAfter) Text(string) error { return nil }
func (s *stopAfter) EndElement() error { return nil }

func TestWalkLongDuplicateKeyExactOffset(t *testing.T) {
	k := strings.Repeat("k", 70<<10)
	src := `{"` + k + `":1,"` + k + `":2}`
	_, parseErr := parse([]byte(src))
	want := `json: duplicate key "` + k[:64] + `"…(71680 bytes) at byte offset ` + fmt.Sprint(2+len(k)+4)
	if errText(parseErr) != want {
		t.Fatalf("Parse: got %.60q…, want the opening quote at %d", errText(parseErr), 2+len(k)+4)
	}
	for _, mode := range []string{"whole", "one-byte"} {
		r := strings.NewReader(src)
		var err error
		if mode == "whole" {
			err = Walk(r, &recorder{})
		} else {
			err = Walk(iotest.OneByteReader(r), &recorder{})
		}
		if errText(err) != want {
			t.Errorf("Walk (%s) differs from Parse: …%s", mode, errText(err)[len(errText(err))-40:])
		}
	}
}

func TestKeySpanTrackingStaysBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 200000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"k%d":"v"}`, i)
	}
	b.WriteString(`]}`)
	vr := newValidatingReader(strings.NewReader(b.String()))
	h := &spanPeek{vr: vr}
	if err := walk(vr, h); err != nil {
		t.Fatal(err)
	}
	if h.max > 4096 {
		t.Fatalf("tracked up to %d open string spans; tracking must stay within the decoder's read-ahead", h.max)
	}
}

type spanPeek struct {
	vr  *validatingReader
	max int
}

func (s *spanPeek) StartElement(string, Kind) error {
	if n := len(s.vr.spans); n > s.max {
		s.max = n
	}
	return nil
}
func (s *spanPeek) Text(string) error { return nil }
func (s *spanPeek) EndElement() error { return nil }
