package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeList(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "inputs.list")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write list: %v", err)
	}
	return p
}

const testHex64 = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// TestReadFileListRefs covers the batch file-list parser: blank/comment lines ignored,
// relative local entries resolved against the LIST FILE'S directory, absolute + s3://
// entries verbatim, order preserved.
func TestReadFileListRefs(t *testing.T) {
	dir := t.TempDir()
	list := writeList(t, dir, strings.Join([]string{
		"# a comment",
		"",
		"   ",
		"a.xml",
		"sub/b.xml",
		"  spaced.xml  ",
		"/abs/c.xml",
		"s3://bucket/prefix/d.xml",
		"# trailing comment",
	}, "\n")+"\n")

	entries, err := readFileListRefs(list)
	if err != nil {
		t.Fatalf("readFileListRefs: %v", err)
	}
	want := []string{
		filepath.Join(dir, "a.xml"),
		filepath.Join(dir, "sub/b.xml"),
		filepath.Join(dir, "spaced.xml"),
		"/abs/c.xml",
		"s3://bucket/prefix/d.xml",
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %#v, want %#v", entries, want)
	}
	for i := range want {
		if entries[i].Ref != want[i] {
			t.Errorf("entry[%d].Ref = %q, want %q (order preserved, relative vs list dir)", i, entries[i].Ref, want[i])
		}
		if entries[i].Declaration != nil {
			t.Errorf("entry[%d] from a URI-only list must carry no declaration", i)
		}
	}
}

func TestReadFileListRefsEmptyFailsLoud(t *testing.T) {
	dir := t.TempDir()
	list := writeList(t, dir, "# only a comment\n\n   \n")
	if _, err := readFileListRefs(list); err == nil || !strings.Contains(err.Error(), "no input references") {
		t.Fatalf("err = %v, want empty-list error", err)
	}
}

func TestReadFileListRefsUnsupportedSchemeFailsLoud(t *testing.T) {
	dir := t.TempDir()
	list := writeList(t, dir, "ok.xml\ngs://bucket/x.xml\n")
	_, err := readFileListRefs(list)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want per-line error naming line 2 for the unsupported scheme", err)
	}
}

func TestReadFileListRefsMissingFileFailsLoud(t *testing.T) {
	if _, err := readFileListRefs(filepath.Join(t.TempDir(), "nope.list")); err == nil || !strings.Contains(err.Error(), "read --file-list") {
		t.Fatalf("err = %v, want read error for a missing list file", err)
	}
}

// TestReadFileListRefsURIsPassVerbatim guards against mangling scheme-bearing entries:
// file:// and s3:// URIs must reach the read boundary unchanged (not be joined to the
// list-file directory), exactly like --files entries.
func TestReadFileListRefsURIsPassVerbatim(t *testing.T) {
	dir := t.TempDir()
	list := writeList(t, dir, "file:///tmp/abs/a.xml\ns3://bucket/k/b.xml\nrel.xml\n")
	entries, err := readFileListRefs(list)
	if err != nil {
		t.Fatalf("readFileListRefs: %v", err)
	}
	want := []string{"file:///tmp/abs/a.xml", "s3://bucket/k/b.xml", filepath.Join(dir, "rel.xml")}
	for i := range want {
		if entries[i].Ref != want[i] {
			t.Errorf("entry[%d].Ref = %q, want %q (URIs verbatim; only bare relative paths resolved)", i, entries[i].Ref, want[i])
		}
	}
}

// TestReferencesIncludeCloudFileList pins that the cloud-session-need check reads
// file-list entries (so s3:// refs in a --file-list create a session and are not
// acquired through the sessionless local boundary).
func TestReferencesIncludeCloudFileList(t *testing.T) {
	dir := t.TempDir()
	cloudList := writeList(t, dir, "local.xml\ns3://bucket/k/remote.xml\n")
	got, err := referencesIncludeCloud(&ExtractOptions{FileList: cloudList})
	if err != nil {
		t.Fatalf("referencesIncludeCloud(cloud list): %v", err)
	}
	if !got {
		t.Error("a --file-list with an s3:// entry must report cloud (needs a session)")
	}

	localList := filepath.Join(dir, "local.list")
	if err := os.WriteFile(localList, []byte("a.xml\nb.xml\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = referencesIncludeCloud(&ExtractOptions{FileList: localList})
	if err != nil {
		t.Fatalf("referencesIncludeCloud(local list): %v", err)
	}
	if got {
		t.Error("a local-only --file-list must not report cloud")
	}
}

// TestReadFileListRefsObjectLines covers the additive integrity-bound form: mixed
// URI-only and object lines keep listed order; object declarations normalize to
// the canonical sha256: form and resolve relative local URIs against the list dir.
func TestReadFileListRefsObjectLines(t *testing.T) {
	dir := t.TempDir()
	list := writeList(t, dir, strings.Join([]string{
		"plain.xml",
		`{"uri":"bound.xml","size":42,"sha256":"` + testHex64 + `"}`,
		`{"uri":"prefixed.xml","size":7,"sha256":"sha256:` + testHex64 + `"}`,
		"s3://bucket/k/object.xml",
	}, "\n")+"\n")

	entries, err := readFileListRefs(list)
	if err != nil {
		t.Fatalf("readFileListRefs: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4 (mixed order preserved)", len(entries))
	}
	if entries[0].Ref != filepath.Join(dir, "plain.xml") || entries[0].Declaration != nil {
		t.Errorf("entry[0] = %#v, want URI-only plain.xml", entries[0])
	}
	if entries[1].Ref != filepath.Join(dir, "bound.xml") {
		t.Errorf("entry[1].Ref = %q, want list-dir-relative resolution", entries[1].Ref)
	}
	if entries[1].Declaration == nil || entries[1].Declaration.Size != 42 || entries[1].Declaration.SHA256 != "sha256:"+testHex64 {
		t.Errorf("entry[1].Declaration = %#v, want size 42 and normalized sha256: prefix", entries[1].Declaration)
	}
	if entries[2].Declaration == nil || entries[2].Declaration.Size != 7 || entries[2].Declaration.SHA256 != "sha256:"+testHex64 {
		t.Errorf("entry[2].Declaration = %#v, want prefixed form preserved and size 7", entries[2].Declaration)
	}
	if entries[3].Ref != "s3://bucket/k/object.xml" || entries[3].Declaration != nil {
		t.Errorf("entry[3] = %#v, want URI-only s3 entry verbatim", entries[3])
	}
}

// TestReadFileListRefsObjectLineStrictFailures pins the fail-closed decode matrix:
// every malformed, ambiguous, or unknown-field object line is a loud per-line error.
func TestReadFileListRefsObjectLineStrictFailures(t *testing.T) {
	good := `"uri":"a.xml","size":1,"sha256":"` + testHex64 + `"`
	cases := []struct {
		name    string
		line    string
		wantSub string
	}{
		{"missing sha256", `{"uri":"a.xml","size":1}`, "sha256 is required"},
		{"missing size", `{"uri":"a.xml","sha256":"` + testHex64 + `"}`, "size is required"},
		{"missing uri", `{"size":1,"sha256":"` + testHex64 + `"}`, "uri is required"},
		{"unknown field version_id", `{` + good + `,"version_id":"v1"}`, `unknown field "version_id"`},
		{"unknown field other", `{` + good + `,"etag":"abc"}`, `unknown field "etag"`},
		{"duplicate field", `{"uri":"a.xml","size":1,"size":2,"sha256":"` + testHex64 + `"}`, `duplicate field "size"`},
		{"fractional size", `{"uri":"a.xml","size":1.5,"sha256":"` + testHex64 + `"}`, "non-negative integer"},
		{"negative size", `{"uri":"a.xml","size":-1,"sha256":"` + testHex64 + `"}`, "non-negative integer"},
		{"exponent size", `{"uri":"a.xml","size":1e3,"sha256":"` + testHex64 + `"}`, "non-negative integer"},
		{"overflow size", `{"uri":"a.xml","size":99999999999999999999999999,"sha256":"` + testHex64 + `"}`, "out of range"},
		{"null size", `{"uri":"a.xml","size":null,"sha256":"` + testHex64 + `"}`, "non-negative integer"},
		{"null uri", `{"uri":null,"size":1,"sha256":"` + testHex64 + `"}`, "uri must be a non-empty string"},
		{"empty uri", `{"uri":"  ","size":1,"sha256":"` + testHex64 + `"}`, "uri must be a non-empty string"},
		{"null sha256", `{"uri":"a.xml","size":1,"sha256":null}`, "sha256 must be a string"},
		{"uppercase hex", `{"uri":"a.xml","size":1,"sha256":"` + strings.ToUpper(testHex64) + `"}`, "64 lowercase hex"},
		{"short hex", `{"uri":"a.xml","size":1,"sha256":"abc"}`, "64 lowercase hex"},
		{"wrong prefix", `{"uri":"a.xml","size":1,"sha256":"sha512:` + testHex64 + `"}`, "64 lowercase hex"},
		{"object size type", `{"uri":"a.xml","size":{"n":1},"sha256":"` + testHex64 + `"}`, "non-negative integer"},
		{"trailing content", `{` + good + `} trailing`, "trailing content"},
		{"second object", `{` + good + `}{` + good + `}`, "trailing content"},
		{"truncated", `{"uri":"a.xml","size":1,"sha256":"` + testHex64, "unexpected EOF"},
		{"unsupported scheme", `{"uri":"gs://bucket/a.xml","size":1,"sha256":"` + testHex64 + `"}`, "unsupported"},
		{"bare brace", `{`, "invalid object line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			list := writeList(t, dir, "ok.xml\n"+tc.line+"\n")
			_, err := readFileListRefs(list)
			if err == nil {
				t.Fatalf("line %q was accepted; want fail-closed error containing %q", tc.line, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantSub)
			}
			if !strings.Contains(err.Error(), "line 2") {
				t.Fatalf("err = %v, want per-line context naming line 2", err)
			}
		})
	}
}

// TestDeclarationAt pins ordinal mapping: declarations ride the input position, so
// duplicate references cannot cross-apply an expected digest.
func TestDeclarationAt(t *testing.T) {
	d1 := &fileListDeclaration{URI: "/a.xml", Size: 1, SHA256: "sha256:" + testHex64}
	d2 := &fileListDeclaration{URI: "/a.xml", Size: 2, SHA256: "sha256:" + testHex64}
	decls := []*fileListDeclaration{d1, nil, d2}

	if got := declarationAt(decls, 1); got != d1 {
		t.Errorf("ordinal 1 = %#v, want d1", got)
	}
	if got := declarationAt(decls, 2); got != nil {
		t.Errorf("ordinal 2 = %#v, want nil (URI-only)", got)
	}
	if got := declarationAt(decls, 3); got != d2 {
		t.Errorf("ordinal 3 = %#v, want d2 (same URI, distinct ordinal)", got)
	}
	if got := declarationAt(decls, 4); got != nil {
		t.Errorf("ordinal 4 = %#v, want nil", got)
	}
	if got := declarationAt(nil, 1); got != nil {
		t.Errorf("nil declarations = %#v, want nil", got)
	}
}
