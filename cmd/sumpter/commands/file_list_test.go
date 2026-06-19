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

	refs, err := readFileListRefs(list)
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
	if len(refs) != len(want) {
		t.Fatalf("refs = %#v, want %#v", refs, want)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Errorf("ref[%d] = %q, want %q (order preserved, relative vs list dir)", i, refs[i], want[i])
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
	refs, err := readFileListRefs(list)
	if err != nil {
		t.Fatalf("readFileListRefs: %v", err)
	}
	want := []string{"file:///tmp/abs/a.xml", "s3://bucket/k/b.xml", filepath.Join(dir, "rel.xml")}
	for i := range want {
		if refs[i] != want[i] {
			t.Errorf("ref[%d] = %q, want %q (URIs verbatim; only bare relative paths resolved)", i, refs[i], want[i])
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
