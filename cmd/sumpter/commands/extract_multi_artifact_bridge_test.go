package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/dataartifact"
)

func dataArtifactContractBase(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
}

func terminalEvent(t *testing.T, eventsPath string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(eventsPath) // #nosec G304
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var last map[string]interface{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var env map[string]interface{}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("parse event: %v\n%s", err, line)
		}
		last = env
	}
	if last == nil {
		t.Fatal("no events in stream")
	}
	return last
}

func terminalArtifacts(t *testing.T, eventsPath string) []map[string]interface{} {
	t.Helper()
	term := terminalEvent(t, eventsPath)
	data, _ := term["data"].(map[string]interface{})
	if data == nil {
		return nil
	}
	raw, ok := data["artifacts"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("artifacts not array: %#v", raw)
	}
	out := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("artifact not object: %#v", item)
		}
		out = append(out, m)
	}
	return out
}

func readPublishedDescriptor(t *testing.T, outRoot, recipeID string) map[string]interface{} {
	t.Helper()
	path := filepath.Join(outRoot, recipeID, dataartifact.DescriptorFileName)
	raw, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		t.Fatalf("read descriptor %s: %v", path, err)
	}
	var d map[string]interface{}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse descriptor: %v", err)
	}
	return d
}

func TestExtractMultiArtifactBridge_PositiveMatch(t *testing.T) {
	for _, workers := range []int{1, 3} {
		t.Run(workersLabel(workers), func(t *testing.T) {
			ws := writeMultiRecipeWorkspace(t, "summary")
			fileList, _ := writeMultiInputSet(t, 2)
			eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
			outRoot, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
				FileList:             fileList,
				InputWorkers:         workers,
				ProcessRunEventsPath: eventsPath,
				ArtifactDescriptor:   true,
				ArtifactContractBase: dataArtifactContractBase(t),
			}, ws, io.Discard)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			assertSchemaValidStream(t, eventsPath)
			assertOneTerminalLast(t, readEventKinds(t, eventsPath), "completed")

			arts := terminalArtifacts(t, eventsPath)
			if len(arts) != 1 {
				t.Fatalf("want 1 artifact ref, got %d: %#v", len(arts), arts)
			}
			a := arts[0]
			if len(a) != 3 {
				t.Fatalf("artifact keys = %v, want exactly artifact_id/lifecycle/descriptor", a)
			}
			desc := readPublishedDescriptor(t, outRoot, "summary")
			if a["artifact_id"] != desc["artifact_id"] {
				t.Fatalf("artifact_id event=%v descriptor=%v", a["artifact_id"], desc["artifact_id"])
			}
			if a["lifecycle"] != desc["lifecycle"] {
				t.Fatalf("lifecycle event=%v descriptor=%v", a["lifecycle"], desc["lifecycle"])
			}
			wantDesc := desc["artifact_id"].(string) + "#descriptor"
			if a["descriptor"] != wantDesc {
				t.Fatalf("descriptor = %v, want %s", a["descriptor"], wantDesc)
			}
			// Redaction: no recipe slug, paths, schemes in stream.
			raw, _ := os.ReadFile(eventsPath) // #nosec G304
			text := string(raw)
			for _, forbidden := range []string{
				"summary", "artifact-descriptor.json", "s3://", "file://",
				"/Users", "AKIA", outRoot,
			} {
				if strings.Contains(text, forbidden) {
					t.Errorf("stream leaked %q", forbidden)
				}
			}
		})
	}
}

func TestExtractMultiArtifactBridge_DescriptorOffOmitsArtifacts(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	_, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList:             fileList,
		ProcessRunEventsPath: eventsPath,
		ArtifactDescriptor:   false,
	}, ws, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertSchemaValidStream(t, eventsPath)
	arts := terminalArtifacts(t, eventsPath)
	if arts != nil {
		t.Fatalf("want artifacts omitted, got %#v", arts)
	}
	// Explicitly ensure the key is absent (not null / empty array).
	term := terminalEvent(t, eventsPath)
	data, _ := term["data"].(map[string]interface{})
	if _, ok := data["artifacts"]; ok {
		t.Fatalf("artifacts key must be absent: %#v", data)
	}
}

func TestExtractMultiArtifactBridge_ProcessRunOffByteIdentity(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 2)
	contractBase := dataArtifactContractBase(t)

	offRoot := filepath.Join(t.TempDir(), "off")
	if err := runExtractMulti(&multiSharedOptions{
		FileList:             fileList,
		OutputPath:           offRoot,
		RunID:                testMultiRunID,
		OutputMode:           "aggregate",
		InputWorkers:         1,
		ArtifactDescriptor:   true,
		ArtifactContractBase: contractBase,
	}, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("off run: %v", err)
	}

	onRoot := filepath.Join(t.TempDir(), "on")
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	if err := runExtractMulti(&multiSharedOptions{
		FileList:             fileList,
		OutputPath:           onRoot,
		RunID:                testMultiRunID,
		OutputMode:           "aggregate",
		InputWorkers:         1,
		ArtifactDescriptor:   true,
		ArtifactContractBase: contractBase,
		ProcessRunEventsPath: eventsPath,
	}, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("on run: %v", err)
	}

	// Descriptor bytes differ only by generated artifact_id — compare structure
	// fields other than artifact_id. Field catalog + records should match shapes.
	offDesc := readPublishedDescriptor(t, offRoot, "summary")
	onDesc := readPublishedDescriptor(t, onRoot, "summary")
	if offDesc["lifecycle"] != onDesc["lifecycle"] {
		t.Fatalf("lifecycle off=%v on=%v", offDesc["lifecycle"], onDesc["lifecycle"])
	}
	// Provenance manifest path present either way.
	offMan := filepath.Join(offRoot, "summary", "manifest.json")
	onMan := filepath.Join(onRoot, "summary", "manifest.json")
	if _, err := os.Stat(offMan); err != nil {
		t.Fatalf("off manifest: %v", err)
	}
	if _, err := os.Stat(onMan); err != nil {
		t.Fatalf("on manifest: %v", err)
	}
	// Process-run stream exists only when on.
	if _, err := os.Stat(eventsPath); err != nil {
		t.Fatalf("events: %v", err)
	}
	arts := terminalArtifacts(t, eventsPath)
	if len(arts) != 1 {
		t.Fatalf("want bridge on process-run path, got %#v", arts)
	}
}

func TestExtractMultiArtifactBridge_MultiRecipeOrder(t *testing.T) {
	for _, workers := range []int{1, 2} {
		t.Run(workersLabel(workers), func(t *testing.T) {
			wsA := writeMultiRecipeWorkspace(t, "summary")
			wsB := writeMultiRecipeWorkspace(t, "line-items")
			fileList, _ := writeMultiInputSet(t, 2)
			outRoot := filepath.Join(t.TempDir(), "out")
			eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
			if err := runExtractMulti(&multiSharedOptions{
				FileList:             fileList,
				OutputPath:           outRoot,
				RunID:                testMultiRunID,
				OutputMode:           "aggregate",
				InputWorkers:         workers,
				ProcessRunEventsPath: eventsPath,
				ArtifactDescriptor:   true,
				ArtifactContractBase: dataArtifactContractBase(t),
			}, []string{wsA, wsB}, io.Discard, time.Now()); err != nil {
				t.Fatalf("run: %v", err)
			}
			assertSchemaValidStream(t, eventsPath)
			arts := terminalArtifacts(t, eventsPath)
			if len(arts) != 2 {
				t.Fatalf("want 2 refs, got %d: %#v", len(arts), arts)
			}
			// Plan order: summary then line-items.
			dA := readPublishedDescriptor(t, outRoot, "summary")
			dB := readPublishedDescriptor(t, outRoot, "line-items")
			if arts[0]["artifact_id"] != dA["artifact_id"] {
				t.Fatalf("first ref not summary: event=%v desc=%v", arts[0]["artifact_id"], dA["artifact_id"])
			}
			if arts[1]["artifact_id"] != dB["artifact_id"] {
				t.Fatalf("second ref not line-items: event=%v desc=%v", arts[1]["artifact_id"], dB["artifact_id"])
			}
			// No recipe identity in stream.
			raw, _ := os.ReadFile(eventsPath) // #nosec G304
			text := string(raw)
			for _, forbidden := range []string{"summary", "line-items", "s3://", outRoot} {
				if strings.Contains(text, forbidden) {
					t.Errorf("stream leaked %q", forbidden)
				}
			}
		})
	}
}

func TestExtractMultiArtifactBridge_PartialIndependentClaims(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	dir := t.TempDir()
	good := filepath.Join(dir, "good.xml")
	if err := os.WriteFile(good, []byte(sampleInputXML("A")), 0o600); err != nil {
		t.Fatalf("write good: %v", err)
	}
	bad := filepath.Join(dir, "bad.xml")
	if err := os.WriteFile(bad, []byte(`<root><unclosed`), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	list := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(list, []byte(good+"\n"+bad+"\n"), 0o600); err != nil {
		t.Fatalf("write list: %v", err)
	}
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	outRoot, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList:             list,
		ContinueOnError:      true,
		ProcessRunEventsPath: eventsPath,
		ArtifactDescriptor:   true,
		ArtifactContractBase: dataArtifactContractBase(t),
	}, ws, io.Discard)
	if err == nil {
		t.Fatal("expected partial failure error")
	}
	assertSchemaValidStream(t, eventsPath)
	kinds := readEventKinds(t, eventsPath)
	assertOneTerminalLast(t, kinds, "failed")
	term := terminalEvent(t, eventsPath)
	data, _ := term["data"].(map[string]interface{})
	if data["reason"] != "partial" {
		t.Fatalf("process reason = %v, want partial", data["reason"])
	}
	arts := terminalArtifacts(t, eventsPath)
	if len(arts) != 1 {
		t.Fatalf("want 1 published partial artifact, got %#v", arts)
	}
	desc := readPublishedDescriptor(t, outRoot, "summary")
	if arts[0]["lifecycle"] != desc["lifecycle"] {
		t.Fatalf("lifecycle event=%v desc=%v", arts[0]["lifecycle"], desc["lifecycle"])
	}
	if desc["lifecycle"] != "partial" {
		t.Fatalf("descriptor lifecycle = %v, want partial", desc["lifecycle"])
	}
	// Independent claims: process failed+partial AND artifact partial both present.
	if arts[0]["lifecycle"] != "partial" {
		t.Fatalf("artifact lifecycle = %v, want partial", arts[0]["lifecycle"])
	}
}

func TestExtractMultiArtifactBridge_NoProcessRunNoBridgeWork(t *testing.T) {
	// Descriptor on, process-run off: descriptor still written; no events file.
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	outRoot := filepath.Join(t.TempDir(), "out")
	eventsPath := filepath.Join(t.TempDir(), "must-not-exist.ndjson")
	if err := runExtractMulti(&multiSharedOptions{
		FileList:             fileList,
		OutputPath:           outRoot,
		RunID:                testMultiRunID,
		OutputMode:           "aggregate",
		ArtifactDescriptor:   true,
		ArtifactContractBase: dataArtifactContractBase(t),
		// ProcessRunEventsPath empty, ProcessRun false
	}, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "summary", dataartifact.DescriptorFileName)); err != nil {
		t.Fatalf("descriptor missing: %v", err)
	}
	if _, err := os.Stat(eventsPath); !os.IsNotExist(err) {
		t.Fatalf("events must not exist; err=%v", err)
	}
}
