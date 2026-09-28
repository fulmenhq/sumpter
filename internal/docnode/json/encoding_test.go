package json

import (
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

// both runs src through Parse and through Walk, whole and one byte at a time,
// and requires the same outcome from all three. encoding/json rescans buffered
// whitespace on every refill, so a one-byte feed through a long run of spaces
// is quadratic; those inputs are fed whole, and oneByteMirror covers the same
// shapes at a smaller size.
func both(t *testing.T, name, src string) string {
	t.Helper()
	_, perr := parse([]byte(src))
	werr := Walk(strings.NewReader(src), &recorder{})
	oerr := werr
	if !strings.Contains(src, strings.Repeat(" ", 1<<16)) {
		oerr = Walk(iotest.OneByteReader(strings.NewReader(src)), &recorder{})
	}
	if errText(perr) != errText(werr) || errText(perr) != errText(oerr) {
		t.Fatalf("%s: Parse %.80q, Walk %.80q, one-byte Walk %.80q", name, errText(perr), errText(werr), errText(oerr))
	}
	return errText(perr)
}

func TestFirstFaultInByteOrder(t *testing.T) {
	far := strings.Repeat("x", 1<<20)
	for _, tc := range []struct {
		name, src, want string
	}{
		{"syntax then UTF-8, same chunk", "{\"a\":tru, \"b\":\"\xff\"}", "json: invalid character ',' in literal true (expecting 'e')"},
		{"syntax then UTF-8, 1 MiB apart", "{\"a\":tru, \"b\":\"" + far + "\xff\"}", "json: invalid character ',' in literal true (expecting 'e')"},
		{"UTF-8 then syntax, same chunk", "{\"b\":\"\xff\", \"a\":tru}", "json: invalid UTF-8 at byte offset 6"},
		{"UTF-8 then syntax, 1 MiB apart", "{\"b\":\"\xff" + far + "\", \"a\":tru}", "json: invalid UTF-8 at byte offset 6"},
		{"trailing byte that is also invalid UTF-8", "{}\xff", "json: invalid UTF-8 at byte offset 2"},
	} {
		if got := both(t, tc.name, tc.src); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

var oneByteMirror = strings.Repeat(" ", 4096)

func TestInvalidUTF8ValidatedThroughEOF(t *testing.T) {
	pad := strings.Repeat(" ", 1<<20)
	late := strings.Repeat("x", 1<<20)
	for _, tc := range []struct {
		name, src, want string
	}{
		{"value then invalid byte", "{\"a\":1}\xff", "json: invalid UTF-8 at byte offset 7"},
		{"value, 1 MiB spaces, invalid byte", "{\"a\":1}" + pad + "\xff", "json: invalid UTF-8 at byte offset 1048583"},
		{"late string is the only fault", "{\"a\":\"" + late + "\xff\"}", "json: invalid UTF-8 at byte offset 1048582"},
		{"value, 1 MiB spaces, truncated rune at EOF", "{\"a\":1}" + pad + "\xe4\xb8", "json: invalid UTF-8 at byte offset 1048583"},
		{"invalid first byte", "\xff{}", "json: invalid UTF-8 at byte offset 0"},
		{"whitespace then invalid byte", "  \xff", "json: invalid UTF-8 at byte offset 2"},
		{"one-byte mirror: value, spaces, invalid byte", "{\"a\":1}" + oneByteMirror + "\xff", "json: invalid UTF-8 at byte offset 4103"},
		{"one-byte mirror: value, spaces, truncated rune at EOF", "{\"a\":1}" + oneByteMirror + "\xe4\xb8", "json: invalid UTF-8 at byte offset 4103"},
	} {
		if got := both(t, tc.name, tc.src); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestByteOrderMark(t *testing.T) {
	const doc = `{"a":{"b":[1,2]},"c":"x"}`
	plain, err := parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	withBOM, err := parse([]byte("\xef\xbb\xbf" + doc))
	if err != nil {
		t.Fatalf("UTF-8 BOM rejected: %v", err)
	}
	if plain.stringValue() != withBOM.stringValue() || plain.firstChild.name != withBOM.firstChild.name {
		t.Fatal("BOM changed the parsed tree")
	}
	if got := both(t, "BOM document", "\xef\xbb\xbf"+doc); got != errText(nil) {
		t.Fatalf("Walk rejected a UTF-8 BOM: %v", got)
	}
	// Offsets stay file-relative after the stripped BOM.
	for _, tc := range []struct {
		name, src, want string
	}{
		{"BOM duplicate key", "\xef\xbb\xbf{\"a\":1,\"a\":2}", `json: duplicate key "a" at byte offset 10`},
		{"BOM invalid byte", "\xef\xbb\xbf{\"a\":\"\xff\"}", "json: invalid UTF-8 at byte offset 9"},
		{"BOM trailing data", "\xef\xbb\xbf{} 1", "json: unexpected data after top-level value at byte offset 6"},
		{"BOM only", "\xef\xbb\xbf", "json: empty input"},
	} {
		if got := both(t, tc.name, tc.src); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	// Exactly one BOM is stripped.
	if got := both(t, "two BOMs", "\xef\xbb\xbf\xef\xbb\xbf{}"); !strings.Contains(got, "invalid character") {
		t.Errorf("second BOM accepted or misreported: %q", got)
	}
}

func TestNonUTF8EncodingsRefused(t *testing.T) {
	for name, src := range map[string]string{
		"UTF-16LE BOM":     "\xff\xfe{\x00}\x00",
		"UTF-16BE BOM":     "\xfe\xff\x00{\x00}",
		"UTF-32BE BOM":     "\x00\x00\xfe\xff\x00\x00\x00{",
		"UTF-32LE BOM":     "\xff\xfe\x00\x00{\x00\x00\x00",
		"UTF-16LE, no BOM": "{\x00}\x00",
		"UTF-16BE, no BOM": "\x00{\x00}",
	} {
		if got := both(t, name, src); got != "json: JSON input must be UTF-8" {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestDuplicateKeyTextIsCapped(t *testing.T) {
	// A multibyte key whose 64-byte cut falls inside a rune: 21 three-byte
	// runes are 63 bytes, so the 22nd rune is dropped whole.
	mb := strings.Repeat("語", 30) // 90 bytes
	src := `{"` + mb + `":1,"` + mb + `":2}`
	got := both(t, "multibyte key", src)
	want := `json: duplicate key "` + strings.Repeat("語", 21) + `"…(90 bytes) at byte offset 96`
	if got != want {
		t.Errorf("multibyte key:\n got %q\nwant %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Error("capped key text is not valid UTF-8")
	}

	// Exactly 64 bytes is shown whole; 65 is capped.
	k64 := strings.Repeat("k", 64)
	if got := both(t, "64-byte key", `{"`+k64+`":1,"`+k64+`":2}`); got != `json: duplicate key "`+k64+`" at byte offset 70` {
		t.Errorf("64-byte key: %q", got)
	}
	k65 := strings.Repeat("k", 65)
	if got := both(t, "65-byte key", `{"`+k65+`":1,"`+k65+`":2}`); got != `json: duplicate key "`+k64+`"…(65 bytes) at byte offset 71` {
		t.Errorf("65-byte key: %q", got)
	}

	// Short keys keep today's text.
	if got := both(t, "short key", `{"a":1,"a":2}`); got != `json: duplicate key "a" at byte offset 7` {
		t.Errorf("short key: %q", got)
	}
}

func TestDuplicateKeyOffsetWithEscapes(t *testing.T) {
	// Escaped quotes and backslashes in and before the key, and a later
	// string prefetched by the decoder, do not move the opening quote.
	for _, tc := range []struct {
		name, src, want string
	}{
		{"escaped quote in key", `{"a\"b":1,"a\"b":2}`, `json: duplicate key "a\"b" at byte offset 10`},
		{"escaped backslash ends key", `{"a\\":1,"a\\":2,"z":"later"}`, `json: duplicate key "a\\" at byte offset 9`},
		{"escaped quote in earlier value", `{"v":"x\"y","a":1,"a":2}`, `json: duplicate key "a" at byte offset 18`},
		{"unicode escape spells the key", `{"a":1,"a":2}`, `json: duplicate key "a" at byte offset 7`},
	} {
		if got := both(t, tc.name, tc.src); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
