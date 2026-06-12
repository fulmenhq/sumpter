package commands

import (
	"regexp"
	"strings"
	"testing"

	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
)

// TestExtractSourceFieldsForFileCloud exercises the real source-extraction path
// (extractSourceFieldsForFile, as the extract pipeline calls it) with a cloud
// logical URI, proving relative_path yields the key relative to the s3:// input
// prefix, absolute_path yields the logical URI, and neither leaks a staging path.
// This is the hermetic end-to-end coverage for the cloud source-extraction fix
// (runs in CI; the moto suite provides the live counterpart).
func TestExtractSourceFieldsForFileCloud(t *testing.T) {
	opts := &ExtractOptions{
		InputPath: "s3://bucket/prefix/",
		SourceExtraction: []recipesmanifest.SourceExtractionPattern{
			{Source: recipesmanifest.SourceExtractionRelativePath, CompiledPattern: regexp.MustCompile(`^(?P<rel>.+)$`)},
			{Source: recipesmanifest.SourceExtractionAbsolutePath, CompiledPattern: regexp.MustCompile(`^(?P<abs>s3://.+)$`)},
			{Source: recipesmanifest.SourceExtractionFilename, CompiledPattern: regexp.MustCompile(`^(?P<name>.+)$`)},
		},
		SourceExtractionRequired: []string{"rel", "abs", "name"},
	}
	limiter := newSourceExtractionWarnLimiter(10)

	fields, err := extractSourceFieldsForFile("s3://bucket/prefix/sub/a.xml", opts, limiter)
	if err != nil {
		t.Fatalf("extractSourceFieldsForFile(cloud) error = %v", err)
	}
	want := map[string]string{
		"rel":  "sub/a.xml",
		"abs":  "s3://bucket/prefix/sub/a.xml",
		"name": "a.xml",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("source field %q = %q, want %q", k, fields[k], v)
		}
	}
	for k, v := range fields {
		if strings.Contains(v, "/work/cloud/") || strings.Contains(v, "/tmp/") {
			t.Errorf("source field %q leaked a local path: %q", k, v)
		}
	}
}

// TestSourceExtractionTargetCloud proves source_extraction derives its match
// target from the logical s3:// identity (never a staged local path) for cloud
// inputs: filename is the key basename, absolute_path is the logical URI, and
// relative_path is the object key relative to the s3:// input prefix.
func TestSourceExtractionTargetCloud(t *testing.T) {
	const root = "s3://bucket/prefix/"
	const file = "s3://bucket/prefix/sub/a.xml"
	input := recipesmanifest.InputDefaults{Path: root}

	cases := []struct {
		name       string
		sourceType string
		want       string
	}{
		{"filename", recipesmanifest.SourceExtractionFilename, "a.xml"},
		{"absolute_path is the logical URI", recipesmanifest.SourceExtractionAbsolutePath, file},
		{"relative_path is key-relative", recipesmanifest.SourceExtractionRelativePath, "sub/a.xml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sourceExtractionTarget(file, tc.sourceType, input)
			if err != nil {
				t.Fatalf("sourceExtractionTarget(%s) error = %v", tc.sourceType, err)
			}
			if got != tc.want {
				t.Errorf("sourceExtractionTarget(%s) = %q, want %q", tc.sourceType, got, tc.want)
			}
			// No source-extraction target may carry a local staging path.
			if strings.Contains(got, "/work/cloud/") || strings.Contains(got, "/tmp/") {
				t.Errorf("sourceExtractionTarget(%s) leaked a local path: %q", tc.sourceType, got)
			}
		})
	}
}

// TestSourceExtractionRelativePathCloudEscape proves a cloud object outside the
// input root's bucket/prefix is rejected as escaping the root, matching the
// local relative_path containment behavior.
func TestSourceExtractionRelativePathCloudEscape(t *testing.T) {
	cases := []struct {
		name string
		root string
		file string
	}{
		{"different bucket", "s3://bucket/prefix/", "s3://other/prefix/a.xml"},
		{"outside prefix", "s3://bucket/prefix/", "s3://bucket/elsewhere/a.xml"},
		{"prefix boundary not a path component", "s3://bucket/prefix/", "s3://bucket/prefixsuffix/a.xml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := recipesmanifest.InputDefaults{Path: tc.root}
			if _, err := sourceExtractionTarget(tc.file, recipesmanifest.SourceExtractionRelativePath, input); err == nil {
				t.Errorf("expected escape error for file %q under root %q", tc.file, tc.root)
			}
		})
	}
}

// TestSourceExtractionTargetLocalUnchanged confirms local sources keep their
// historical filesystem-path behavior (filename basename), so the cloud branch
// does not perturb the local path.
func TestSourceExtractionTargetLocalUnchanged(t *testing.T) {
	got, err := sourceExtractionTarget("/data/in/a.xml", recipesmanifest.SourceExtractionFilename, recipesmanifest.InputDefaults{})
	if err != nil {
		t.Fatalf("sourceExtractionTarget(local filename) error = %v", err)
	}
	if got != "a.xml" {
		t.Errorf("local filename = %q, want a.xml", got)
	}
}
