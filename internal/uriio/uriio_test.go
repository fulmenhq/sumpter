package uriio_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

func TestClassifyLocalBarePath(t *testing.T) {
	cases := []string{
		"data/input.xml",
		"./relative/path.xml",
		"/absolute/path.xml",
		"input.xml.gz",
		`C:\windows\style\path.xml`, // colon but no "://" — bare local path
	}
	for _, raw := range cases {
		ref, err := uriio.Classify(raw)
		if err != nil {
			t.Fatalf("Classify(%q) returned error: %v", raw, err)
		}
		if ref.Scheme != uriio.SchemeLocal {
			t.Errorf("Classify(%q) scheme = %s, want local", raw, ref.Scheme)
		}
		if ref.LocalPath != raw {
			t.Errorf("Classify(%q) LocalPath = %q, want %q", raw, ref.LocalPath, raw)
		}
		if ref.IsCloud() {
			t.Errorf("Classify(%q) IsCloud = true, want false", raw)
		}
	}
}

func TestClassifyFileURI(t *testing.T) {
	ref, err := uriio.Classify("file:///var/data/input.xml")
	if err != nil {
		t.Fatalf("Classify(file://) error: %v", err)
	}
	if ref.Scheme != uriio.SchemeLocal {
		t.Errorf("scheme = %s, want local", ref.Scheme)
	}
	if ref.LocalPath != filepath.FromSlash("/var/data/input.xml") {
		t.Errorf("LocalPath = %q, want /var/data/input.xml", ref.LocalPath)
	}
}

func TestClassifyS3URI(t *testing.T) {
	ref, err := uriio.Classify("s3://bucket/prefix/object.xml")
	if err != nil {
		t.Fatalf("Classify(s3://) error: %v", err)
	}
	if ref.Scheme != uriio.SchemeS3 {
		t.Errorf("scheme = %s, want s3", ref.Scheme)
	}
	if ref.Bucket != "bucket" {
		t.Errorf("Bucket = %q, want bucket", ref.Bucket)
	}
	if ref.Key != "prefix/object.xml" {
		t.Errorf("Key = %q, want prefix/object.xml", ref.Key)
	}
	if !ref.IsCloud() {
		t.Error("IsCloud = false, want true")
	}
}

func TestClassifyS3Pattern(t *testing.T) {
	ref, err := uriio.Classify("s3://bucket/prefix/**/*.xml")
	if err != nil {
		t.Fatalf("Classify(s3 pattern) error: %v", err)
	}
	if ref.Pattern == "" {
		t.Error("Pattern is empty, want the glob preserved")
	}
	if ref.Key != "prefix/" {
		t.Errorf("Key (listing prefix) = %q, want prefix/", ref.Key)
	}
}

func TestClassifyEmptyReference(t *testing.T) {
	_, err := uriio.Classify("")
	if !errors.Is(err, uriio.ErrEmptyReference) {
		t.Errorf("Classify(\"\") error = %v, want ErrEmptyReference", err)
	}
}

func TestClassifyUnsupportedScheme(t *testing.T) {
	for _, raw := range []string{"gs://bucket/key", "azblob://container/blob"} {
		_, err := uriio.Classify(raw)
		if err == nil {
			t.Errorf("Classify(%q) error = nil, want unsupported/parse error", raw)
		}
	}
}

func TestAcquireLocalPassthrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.xml")
	if err := os.WriteFile(path, []byte("<root/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	src, err := uriio.Acquire(context.Background(), uriio.AcquireRequest{Reference: path})
	if err != nil {
		t.Fatalf("Acquire(local) error: %v", err)
	}
	if src.LocalPath != path {
		t.Errorf("LocalPath = %q, want %q (pass-through)", src.LocalPath, path)
	}
	if src.LogicalURI != path {
		t.Errorf("LogicalURI = %q, want %q", src.LogicalURI, path)
	}
	if src.Scheme != uriio.SchemeLocal {
		t.Errorf("Scheme = %s, want local", src.Scheme)
	}
	// Cleanup is a no-op for local and idempotent.
	if err := src.Cleanup(); err != nil {
		t.Errorf("Cleanup() #1 error: %v", err)
	}
	if err := src.Cleanup(); err != nil {
		t.Errorf("Cleanup() #2 (idempotent) error: %v", err)
	}
}

func TestAcquireS3NotImplemented(t *testing.T) {
	_, err := uriio.Acquire(context.Background(), uriio.AcquireRequest{Reference: "s3://bucket/key.xml"})
	if !errors.Is(err, uriio.ErrSchemeNotImplemented) {
		t.Fatalf("Acquire(s3) error = %v, want ErrSchemeNotImplemented", err)
	}
	// The message must guide the operator without naming internal milestones.
	if msg := err.Error(); !strings.Contains(msg, "s3://") || !strings.Contains(msg, "local path") {
		t.Errorf("error message not actionable: %q", msg)
	}
}

func TestOpenOutputLocalPassthrough(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	tgt, err := uriio.OpenOutput(context.Background(), uriio.OutputRequest{Reference: path})
	if err != nil {
		t.Fatalf("OpenOutput(local) error: %v", err)
	}
	if tgt.LocalPath != path {
		t.Errorf("LocalPath = %q, want %q", tgt.LocalPath, path)
	}
	if tgt.Scheme != uriio.SchemeLocal {
		t.Errorf("Scheme = %s, want local", tgt.Scheme)
	}
	// Write to the resolved local path and publish (no-op for local).
	if err := os.WriteFile(tgt.LocalPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tgt.Publish(context.Background()); err != nil {
		t.Errorf("Publish() local error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected output durable at %q: %v", path, err)
	}
}

func TestOpenOutputS3NotImplemented(t *testing.T) {
	_, err := uriio.OpenOutput(context.Background(), uriio.OutputRequest{Reference: "s3://bucket/out.json"})
	if !errors.Is(err, uriio.ErrSchemeNotImplemented) {
		t.Fatalf("OpenOutput(s3) error = %v, want ErrSchemeNotImplemented", err)
	}
}

func TestListLocalSingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.xml")
	if err := os.WriteFile(path, []byte("<a/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	listing, err := uriio.List(context.Background(), uriio.ListRequest{Reference: path})
	if err != nil {
		t.Fatalf("List(file) error: %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].LocalPath != path {
		t.Fatalf("List(file) entries = %+v, want single %q", listing.Entries, path)
	}
}

func TestListLocalDirectoryRecursive(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "a.xml"))
	mkFile(t, filepath.Join(dir, "sub", "b.xml"))

	// Non-recursive: only the top-level file.
	flat, err := uriio.List(context.Background(), uriio.ListRequest{Reference: dir, Recursive: false})
	if err != nil {
		t.Fatalf("List(dir, non-recursive) error: %v", err)
	}
	if got := names(flat); !equal(got, []string{"a.xml"}) {
		t.Errorf("non-recursive entries = %v, want [a.xml]", got)
	}

	// Recursive: both files.
	deep, err := uriio.List(context.Background(), uriio.ListRequest{Reference: dir, Recursive: true})
	if err != nil {
		t.Fatalf("List(dir, recursive) error: %v", err)
	}
	if got := names(deep); !equal(got, []string{"a.xml", "b.xml"}) {
		t.Errorf("recursive entries = %v, want [a.xml b.xml]", got)
	}
}

func TestListS3NotImplemented(t *testing.T) {
	_, err := uriio.List(context.Background(), uriio.ListRequest{Reference: "s3://bucket/prefix/"})
	if !errors.Is(err, uriio.ErrSchemeNotImplemented) {
		t.Fatalf("List(s3) error = %v, want ErrSchemeNotImplemented", err)
	}
}

// --- helpers ---

func mkFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("<x/>"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(l *uriio.Listing) []string {
	out := make([]string, 0, len(l.Entries))
	for _, e := range l.Entries {
		out = append(out, filepath.Base(e.LocalPath))
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
