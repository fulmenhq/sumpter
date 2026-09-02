package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/uriio"
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

func TestExtractMultiEagerBoundedLocalParity(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 4)
	run := func(mode string) (records, manifest string) {
		t.Helper()
		out := t.TempDir()
		shared := &multiSharedOptions{
			FileList:             fileList,
			OutputPath:           out,
			OutputMode:           outputModeAggregate,
			RunID:                testMultiRunID,
			CloudInputMode:       mode,
			CloudStagingMaxBytes: 1 << 20,
			CloudStagingMaxFiles: 4,
			CloudObjectMaxBytes:  1 << 20,
		}
		if err := runExtractMulti(shared, []string{ws}, os.Stderr, time.Now()); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		rec, err := os.ReadFile(filepath.Join(out, "summary", "records.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		man, err := os.ReadFile(filepath.Join(out, "summary", "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		return volatileGeneratedAtRE.ReplaceAllString(string(rec), `"generated_at":""`), string(man)
	}
	eagerRec, eagerMan := run(cloudInputModeEager)
	boundedRec, boundedMan := run(cloudInputModeBounded)
	if eagerRec != boundedRec {
		t.Fatalf("eager/bounded record bytes differ\neager=%s\nbounded=%s", eagerRec, boundedRec)
	}
	var eagerM, boundedM provenance.Manifest
	if err := json.Unmarshal([]byte(eagerMan), &eagerM); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(boundedMan), &boundedM); err != nil {
		t.Fatal(err)
	}
	if len(eagerM.Inputs) != len(boundedM.Inputs) {
		t.Fatalf("input count eager=%d bounded=%d", len(eagerM.Inputs), len(boundedM.Inputs))
	}
	for i := range eagerM.Inputs {
		ei, bi := eagerM.Inputs[i], boundedM.Inputs[i]
		if ei.Path != bi.Path || ei.SHA256 != bi.SHA256 || ei.SizeBytes != bi.SizeBytes || ei.Disposition != bi.Disposition {
			t.Fatalf("input %d identity differ: %+v vs %+v", i, ei, bi)
		}
		if (ei.RecordCount == nil) != (bi.RecordCount == nil) {
			t.Fatalf("input %d record_count nilness", i)
		}
		if ei.RecordCount != nil && *ei.RecordCount != *bi.RecordCount {
			t.Fatalf("input %d record_count %d vs %d", i, *ei.RecordCount, *bi.RecordCount)
		}
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

func TestExtractMultiCloudPrefetchCapsConcurrentPrepare(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 8)
	shared := &multiSharedOptions{
		FileList:             fileList,
		OutputPath:           t.TempDir(),
		OutputMode:           outputModeAggregate,
		RunID:                testMultiRunID,
		InputWorkers:         4,
		CloudInputMode:       cloudInputModeBounded,
		CloudPrefetch:        1,
		CloudStagingMaxBytes: 1 << 20,
		CloudStagingMaxFiles: 8,
		CloudObjectMaxBytes:  1 << 20,
	}
	d := newMultiDispatcher(shared, io.Discard)
	var concurrent, peak int32
	release := make(chan struct{})
	var entered int32
	d.onPrepareInput = func(enter bool) {
		if enter {
			c := atomic.AddInt32(&concurrent, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if c <= p || atomic.CompareAndSwapInt32(&peak, p, c) {
					break
				}
			}
			n := atomic.AddInt32(&entered, 1)
			if n == 1 {
				<-release
			}
			atomic.AddInt32(&concurrent, -1)
		}
	}
	done := make(chan error, 1)
	go func() { done <- d.run([]string{ws}, time.Now()) }()
	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt32(&peak); got > 1 {
		t.Fatalf("prefetch=1 allowed peak prepare %d", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&peak); got != 1 {
		t.Fatalf("peak=%d want 1", got)
	}
}

func TestExtractMultiBoundedReapAfterBundleBeforeCommit(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 4)
	shared := &multiSharedOptions{
		FileList:             fileList,
		OutputPath:           t.TempDir(),
		OutputMode:           outputModeAggregate,
		RunID:                testMultiRunID,
		InputWorkers:         2,
		CloudInputMode:       cloudInputModeBounded,
		CloudPrefetch:        1,
		CloudStagingMaxBytes: 1 << 20,
		CloudStagingMaxFiles: 4,
		CloudObjectMaxBytes:  1 << 20,
	}
	d := newMultiDispatcher(shared, io.Discard)
	var held int32
	var heldDuringBuild int32
	d.onPrepareInput = func(enter bool) {
		if enter {
			atomic.AddInt32(&held, 1)
		} else {
			atomic.AddInt32(&held, -1)
		}
	}
	d.onBuildApplication = func(ordinal int) {
		if atomic.LoadInt32(&held) >= 1 {
			atomic.StoreInt32(&heldDuringBuild, 1)
		}
	}
	if err := d.run([]string{ws}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&heldDuringBuild) != 1 {
		t.Fatal("acquire window was not held through bundle construction")
	}
	if atomic.LoadInt32(&held) != 0 {
		t.Fatalf("acquire window not reaped after run: held=%d", atomic.LoadInt32(&held))
	}
}

func TestFormatStagingStatsHasNoURI(t *testing.T) {
	out := formatStagingStats(uriio.StagingStats{PeakFiles: 2, PeakBytes: 99, AcquiredCount: 3})
	if strings.Contains(out, "s3://") || strings.Contains(strings.ToLower(out), "http") {
		t.Fatalf("stats leaked identity: %q", out)
	}
	if !strings.Contains(out, "peak_files=2") || !strings.Contains(out, "peak_bytes=99") {
		t.Fatalf("stats %q", out)
	}
}
