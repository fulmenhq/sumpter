package commands

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
)

func TestDiscoverInputFiles(t *testing.T) {
	t.Parallel()

	tempDir := createWorkingTempDir(t)

	included := filepath.Join(tempDir, "sample.xml")
	if err := os.WriteFile(included, []byte("<root/>"), 0o644); err != nil {
		t.Fatalf("failed to write include fixture: %v", err)
	}

	excluded := filepath.Join(tempDir, "ignore.txt")
	if err := os.WriteFile(excluded, []byte("skip"), 0o644); err != nil {
		t.Fatalf("failed to write exclude fixture: %v", err)
	}

	opts := &ExtractOptions{
		InputPath:      tempDir,
		IncludePattern: "*.xml",
		ExcludePattern: "*.bak",
	}

	files, err := discoverInputFiles(opts)
	if err != nil {
		t.Fatalf("discoverInputFiles directory scan error: %v", err)
	}

	sort.Strings(files)
	if len(files) != 1 || files[0] != included {
		t.Fatalf("unexpected discovery result: %v", files)
	}

	fileOnly := &ExtractOptions{
		InputPath:      included,
		IncludePattern: "*.xml",
	}

	files, err = discoverInputFiles(fileOnly)
	if err != nil {
		t.Fatalf("discoverInputFiles file scan error: %v", err)
	}

	if len(files) != 1 || files[0] != included {
		t.Fatalf("unexpected single file result: %v", files)
	}
}

func TestBuildExternalFieldsMergeOrder(t *testing.T) {
	fields, err := buildExternalFields(&ExtractOptions{
		ClientID:           "client-flag",
		SiteID:             "site-flag",
		ManifestParameters: map[string]string{"client_id": "client-manifest", "region_id": "west"},
		Parameters:         []string{"region_id=east", "tenant_id=tenant-1"},
		ParametersRequired: []string{"client_id", "region_id", "tenant_id", "site_id"},
	}, nil)
	if err != nil {
		t.Fatalf("buildExternalFields: %v", err)
	}

	want := map[string]interface{}{
		"client_id": "client-manifest",
		"site_id":   "site-flag",
		"region_id": "east",
		"tenant_id": "tenant-1",
	}
	for key, value := range want {
		if fields[key] != value {
			t.Fatalf("%s = %#v, want %#v (all fields: %#v)", key, fields[key], value, fields)
		}
	}
}

func TestBuildExternalFieldsRequiredFailure(t *testing.T) {
	_, err := buildExternalFields(&ExtractOptions{
		ManifestParameters: map[string]string{"region_id": "west"},
		ParametersRequired: []string{"tenant_id"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), `required parameter "tenant_id" not provided`) {
		t.Fatalf("expected required parameter error, got %v", err)
	}
}

func TestBuildExternalFieldsEmptyRequiredKey(t *testing.T) {
	_, err := buildExternalFields(&ExtractOptions{ParametersRequired: []string{""}}, nil)
	if err == nil || !strings.Contains(err.Error(), "required parameter key cannot be empty") {
		t.Fatalf("expected empty required key error, got %v", err)
	}
}

func TestBuildExternalFieldsMalformedParameter(t *testing.T) {
	_, err := buildExternalFields(&ExtractOptions{Parameters: []string{"tenant_id"}}, nil)
	if err == nil || !strings.Contains(err.Error(), `invalid --parameter "tenant_id": expected key=value`) {
		t.Fatalf("expected malformed parameter error, got %v", err)
	}
}

func TestBuildExternalFieldsCollisionWithFieldMapping(t *testing.T) {
	mappings := []extract.FieldMapping{{OutputField: "business_date", XPath: "BusinessDate", Type: "string"}}

	_, err := buildExternalFields(&ExtractOptions{
		ManifestParameters: map[string]string{"business_date": "2024-01-01"},
	}, mappings)
	if err == nil || !strings.Contains(err.Error(), `parameter key "business_date" collides with field_mappings output_field`) {
		t.Fatalf("expected manifest collision error, got %v", err)
	}

	_, err = buildExternalFields(&ExtractOptions{
		Parameters: []string{"business_date=2024-01-02"},
	}, mappings)
	if err == nil || !strings.Contains(err.Error(), `parameter key "business_date" collides with field_mappings output_field`) {
		t.Fatalf("expected CLI collision error, got %v", err)
	}
}

func TestExtractSourceFieldsForFileSources(t *testing.T) {
	root := createWorkingTempDir(t)
	file := filepath.Join(root, "sites", "store-17", "2026-05-15-register.xml")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatalf("failed to create fixture directory: %v", err)
	}
	if err := os.WriteFile(file, []byte("<root/>"), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	opts := &ExtractOptions{
		SourceExtraction: []recipesmanifest.SourceExtractionPattern{
			sourcePattern("filename-date-token", recipesmanifest.SourceExtractionFilename, `^(?P<business_date>\d{4}-\d{2}-\d{2})-.*\.xml$`),
			sourcePattern("relative-site", recipesmanifest.SourceExtractionRelativePath, `^sites/(?P<source_site_id>[^/]+)/`),
			sourcePattern("absolute-ext", recipesmanifest.SourceExtractionAbsolutePath, `(?P<extension>\.xml)$`),
		},
		SourceExtractionRequired: []string{"business_date", "source_site_id", "extension"},
		SourceExtractionInput: recipesmanifest.InputDefaults{
			Path: root,
		},
	}

	fields, err := extractSourceFieldsForFile(file, opts, newSourceExtractionWarnLimiter(1000))
	if err != nil {
		t.Fatalf("extractSourceFieldsForFile: %v", err)
	}

	want := map[string]string{
		"business_date":  "2026-05-15",
		"source_site_id": "store-17",
		"extension":      ".xml",
	}
	for key, value := range want {
		if fields[key] != value {
			t.Fatalf("%s = %#v, want %#v (all fields: %#v)", key, fields[key], value, fields)
		}
	}
}

func TestExtractSourceFieldsRelativePathRequiresExplicitRoot(t *testing.T) {
	opts := &ExtractOptions{
		SourceExtraction: []recipesmanifest.SourceExtractionPattern{
			sourcePattern("relative-site", recipesmanifest.SourceExtractionRelativePath, `^sites/(?P<source_site_id>[^/]+)/`),
		},
	}

	_, err := extractSourceFieldsForFile("sites/store-17/sample.xml", opts, newSourceExtractionWarnLimiter(1000))
	if err == nil || !strings.Contains(err.Error(), "relative_path requires --input-path") {
		t.Fatalf("expected missing relative_path root error, got %v", err)
	}
}

func TestExtractSourceFieldsRelativePathRejectsEscapes(t *testing.T) {
	root := createWorkingTempDir(t)
	outside := filepath.Join(filepath.Dir(root), "outside-source.xml")
	if err := os.WriteFile(outside, []byte("<root/>"), 0o644); err != nil {
		t.Fatalf("failed to write outside fixture: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	opts := &ExtractOptions{
		SourceExtraction: []recipesmanifest.SourceExtractionPattern{
			sourcePattern("relative-site", recipesmanifest.SourceExtractionRelativePath, `(?P<name>.+)`),
		},
		SourceExtractionInput: recipesmanifest.InputDefaults{
			Path: root,
		},
	}

	_, err := extractSourceFieldsForFile(outside, opts, newSourceExtractionWarnLimiter(1000))
	if err == nil || !strings.Contains(err.Error(), "escapes input root") {
		t.Fatalf("expected outside-root error, got %v", err)
	}
}

func TestBuildExternalFieldsForFileMergeOrderAndIsolation(t *testing.T) {
	root := createWorkingTempDir(t)
	fileA := filepath.Join(root, "2026-05-15-a.xml")
	fileB := filepath.Join(root, "2026-05-16-b.xml")
	for _, file := range []string{fileA, fileB} {
		if err := os.WriteFile(file, []byte("<root/>"), 0o644); err != nil {
			t.Fatalf("failed to write fixture %s: %v", file, err)
		}
	}

	opts := &ExtractOptions{
		ClientID: "client-flag",
		SourceExtraction: []recipesmanifest.SourceExtractionPattern{
			sourcePattern("date-first", recipesmanifest.SourceExtractionFilename, `^(?P<business_date>\d{4}-\d{2}-\d{2})-`),
			sourcePattern("date-overwrite", recipesmanifest.SourceExtractionFilename, `^(?P<business_date>\d{4})-`),
			sourcePattern("file-token", recipesmanifest.SourceExtractionFilename, `^\d{4}-\d{2}-\d{2}-(?P<file_token>[a-z])\.xml$`),
		},
		SourceExtractionRequired: []string{"business_date", "file_token"},
		ManifestParameters:       map[string]string{"client_id": "client-manifest", "region_id": "west"},
		Parameters:               []string{"region_id=east"},
		SourceExtractionInput: recipesmanifest.InputDefaults{
			Path: root,
		},
	}
	plan, err := buildExternalFieldPlan(opts, nil)
	if err != nil {
		t.Fatalf("buildExternalFieldPlan: %v", err)
	}

	fieldsA, err := buildExternalFieldsForFile(fileA, opts, plan, newSourceExtractionWarnLimiter(1000))
	if err != nil {
		t.Fatalf("buildExternalFieldsForFile A: %v", err)
	}
	fieldsB, err := buildExternalFieldsForFile(fileB, opts, plan, newSourceExtractionWarnLimiter(1000))
	if err != nil {
		t.Fatalf("buildExternalFieldsForFile B: %v", err)
	}

	if fieldsA["business_date"] != "2026" {
		t.Fatalf("business_date A = %#v, want later-pattern overwrite 2026", fieldsA["business_date"])
	}
	if fieldsB["business_date"] != "2026" {
		t.Fatalf("business_date B = %#v, want per-file 2026", fieldsB["business_date"])
	}
	if fieldsA["file_token"] != "a" || fieldsB["file_token"] != "b" {
		t.Fatalf("file_token isolation failed: A=%#v B=%#v", fieldsA["file_token"], fieldsB["file_token"])
	}
	if fieldsA["region_id"] != "east" || fieldsA["client_id"] != "client-manifest" {
		t.Fatalf("merge order produced %#v", fieldsA)
	}
}

func TestSourceExtractionRequiredCannotBeMaskedByCLIParameter(t *testing.T) {
	opts := &ExtractOptions{
		SourceExtraction: []recipesmanifest.SourceExtractionPattern{
			sourcePattern("date-token", recipesmanifest.SourceExtractionFilename, `^NO_MATCH_(?P<business_date>\d{4}-\d{2}-\d{2})`),
		},
		SourceExtractionRequired: []string{"business_date"},
		Parameters:               []string{"business_date=2026-05-15"},
	}
	plan, err := buildExternalFieldPlan(opts, nil)
	if err != nil {
		t.Fatalf("buildExternalFieldPlan: %v", err)
	}

	_, err = buildExternalFieldsForFile("2026-05-15-register.xml", opts, plan, newSourceExtractionWarnLimiter(1000))
	if err == nil || !strings.Contains(err.Error(), `required source_extraction field "business_date" not provided`) {
		t.Fatalf("expected source_extraction_required failure before CLI merge, got %v", err)
	}
}

func TestValidateSourceExtractionDeclarationsCollisions(t *testing.T) {
	mappings := []extract.FieldMapping{{OutputField: "business_date", XPath: "BusinessDate", Type: "string"}}
	opts := &ExtractOptions{
		SourceExtraction: []recipesmanifest.SourceExtractionPattern{
			sourcePattern("date-token", recipesmanifest.SourceExtractionFilename, `^(?P<business_date>\d{4}-\d{2}-\d{2})`),
		},
	}

	err := validateSourceExtractionDeclarations(opts, mappings)
	if err == nil || !strings.Contains(err.Error(), `source_extraction capture "business_date" collides`) {
		t.Fatalf("expected field mapping collision, got %v", err)
	}

	opts.ManifestParameters = map[string]string{"business_date": "2026-05-15"}
	err = validateSourceExtractionDeclarations(opts, nil)
	if err == nil || !strings.Contains(err.Error(), "collides with defaults.parameters") {
		t.Fatalf("expected defaults.parameters collision, got %v", err)
	}
}

func sourcePattern(id, source, pattern string) recipesmanifest.SourceExtractionPattern {
	return recipesmanifest.SourceExtractionPattern{
		ID:              id,
		Source:          source,
		Pattern:         pattern,
		CompiledPattern: regexp.MustCompile(pattern),
	}
}

func createWorkingTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "extract-test-")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("failed to resolve temp directory: %v", err)
	}
	return abs
}
