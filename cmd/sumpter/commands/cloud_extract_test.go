package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateCloudInputOptionsEagerDefault(t *testing.T) {
	opts := &ExtractOptions{}
	if err := validateCloudInputOptions(opts); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCloudInputOptionsBoundedRequiresCaps(t *testing.T) {
	opts := &ExtractOptions{CloudInputMode: cloudInputModeBounded}
	if err := validateCloudInputOptions(opts); err == nil {
		t.Fatal("expected error")
	}
	opts.FileList = "uris.txt"
	if err := validateCloudInputOptions(opts); err == nil {
		t.Fatal("expected bytes error")
	}
	opts.CloudStagingMaxBytes = 1024
	opts.CloudStagingMaxFiles = 2
	opts.CloudObjectMaxBytes = 512
	if err := validateCloudInputOptions(opts); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCloudInputOptionsRejectsUnknownMode(t *testing.T) {
	opts := &ExtractOptions{CloudInputMode: "stream"}
	if err := validateCloudInputOptions(opts); err == nil {
		t.Fatal("expected unknown mode error")
	}
}

func TestCloudPrefetchDefault(t *testing.T) {
	opts := &ExtractOptions{}
	if got := cloudPrefetchWindow(opts, 4); got != 4 {
		t.Fatalf("got %d", got)
	}
	opts.CloudPrefetch = 2
	if got := cloudPrefetchWindow(opts, 4); got != 2 {
		t.Fatalf("got %d", got)
	}
}

func TestExtractMultiBoundedLocalFileListNoCloudSession(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 3)
	out := t.TempDir()
	shared := &multiSharedOptions{
		FileList:             fileList,
		OutputPath:           out,
		OutputMode:           outputModeAggregate,
		RunID:                testMultiRunID,
		CloudInputMode:       cloudInputModeBounded,
		CloudStagingMaxBytes: 1 << 20,
		CloudStagingMaxFiles: 2,
		CloudObjectMaxBytes:  1 << 20,
	}
	if err := runExtractMulti(shared, []string{ws}, os.Stderr, time.Now()); err != nil {
		t.Fatal(err)
	}
	records := filepath.Join(out, "summary", "records.jsonl")
	data, err := os.ReadFile(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") < 3 {
		t.Fatalf("want at least 3 records, got %q", data)
	}
}
