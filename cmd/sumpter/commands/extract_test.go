package commands

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
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
