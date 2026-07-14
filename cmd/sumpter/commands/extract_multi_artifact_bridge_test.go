package commands

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/dataartifact"
	"github.com/fulmenhq/sumpter/internal/processrun"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/uriio"
)

func dataArtifactContractBase(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
}

func clearArtifactBridgeHooks(t *testing.T) {
	t.Helper()
	prevPub := dataArtifactDescriptorPublishHook
	prevVal := extractOutputValidateHook
	t.Cleanup(func() {
		dataArtifactDescriptorPublishHook = prevPub
		extractOutputValidateHook = prevVal
	})
	dataArtifactDescriptorPublishHook = nil
	extractOutputValidateHook = nil
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

// normalizeArtifactParity blanks per-run UUID fields and wall-clock-derived
// integrity digests (record shards embed generated_at, so whole_digest differs
// across separate runs even when process-run is the only variable).
var artifactParityVolatileRE = regexp.MustCompile(
	`"(artifact_id|run_id)"\s*:\s*"[^"]*"`,
)
var artifactParityDigestRE = regexp.MustCompile(
	`"(value|sha256)"\s*:\s*"(sha256:)?[0-9a-fA-F]{64}"`,
)

func normalizeArtifactParity(b []byte) string {
	s := artifactParityVolatileRE.ReplaceAllString(string(b), `"$1":"<normalized>"`)
	// Record envelopes carry wall-clock generated_at inside _runtime.
	s = volatileGeneratedAtRE.ReplaceAllString(s, `"generated_at":""`)
	s = artifactParityDigestRE.ReplaceAllString(s, `"$1":"<digest>"`)
	return s
}

// snapshotRecipeArtifactSurfaces captures records, descriptor, field catalog, and
// a stable provenance projection for process-run on/off parity. Manifest digests
// that include wall-clock record timestamps are blanked via stableManifest.
func snapshotRecipeArtifactSurfaces(t *testing.T, recipeDir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(recipeDir)
	if err != nil {
		t.Fatalf("readdir %s: %v", recipeDir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			if e.Name() == "fields" {
				catalog := filepath.Join(recipeDir, "fields", "records.fields.json")
				if data, rerr := os.ReadFile(catalog); rerr == nil { // #nosec G304
					out["fields/records.fields.json"] = normalizeArtifactParity(data)
				}
			}
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		path := filepath.Join(recipeDir, n)
		if n == provenance.ManifestFileName {
			// Stable projection: blank timestamp-inclusive shard digests and
			// wall-clock/runtime fields already handled by stableManifest.
			out[n] = stableManifest(t, path)
			continue
		}
		data, err := os.ReadFile(path) // #nosec G304
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		out[n] = normalizeArtifactParity(data)
	}
	return out
}

func assertRecipeSurfacesEqual(t *testing.T, off, on map[string]string) {
	t.Helper()
	if len(off) != len(on) {
		t.Fatalf("surface count off=%d on=%d\noff keys=%v\non keys=%v", len(off), len(on), keysSorted(off), keysSorted(on))
	}
	for k, offV := range off {
		onV, ok := on[k]
		if !ok {
			t.Fatalf("on missing surface %q", k)
		}
		if offV != onV {
			t.Fatalf("surface %q differs process-run off vs on\n--- off ---\n%s\n--- on ---\n%s", k, offV, onV)
		}
	}
}

func keysSorted(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertEventRedactionCorpus(t *testing.T, eventsPath string, forbidden ...string) {
	t.Helper()
	raw, err := os.ReadFile(eventsPath) // #nosec G304
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	text := string(raw)
	for _, f := range forbidden {
		if f == "" {
			continue
		}
		if strings.Contains(text, f) {
			t.Errorf("event stream leaked %q", f)
		}
	}
	// Structural: every artifact descriptor field must be ID-derived only.
	for _, a := range terminalArtifacts(t, eventsPath) {
		id, _ := a["artifact_id"].(string)
		desc, _ := a["descriptor"].(string)
		if !strings.HasPrefix(id, "urn:uuid:") {
			t.Errorf("artifact_id not urn:uuid: %q", id)
		}
		if desc != id+"#descriptor" {
			t.Errorf("descriptor not ID-derived: id=%q desc=%q", id, desc)
		}
		if strings.Contains(desc, "://") || strings.ContainsAny(desc, `/\`) {
			t.Errorf("descriptor looks like a locator: %q", desc)
		}
	}
}

func TestExtractMultiArtifactBridge_PositiveMatch(t *testing.T) {
	clearArtifactBridgeHooks(t)
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
			assertEventRedactionCorpus(t, eventsPath,
				"summary", "artifact-descriptor.json", "s3://", "file://",
				"/Users", "AKIA", outRoot,
			)
		})
	}
}

func TestExtractMultiArtifactBridge_DescriptorOffOmitsArtifacts(t *testing.T) {
	clearArtifactBridgeHooks(t)
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
	term := terminalEvent(t, eventsPath)
	data, _ := term["data"].(map[string]interface{})
	if _, ok := data["artifacts"]; ok {
		t.Fatalf("artifacts key must be absent: %#v", data)
	}
}

func TestExtractMultiArtifactBridge_ProcessRunOffByteIdentity(t *testing.T) {
	clearArtifactBridgeHooks(t)
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

	offSnap := snapshotRecipeArtifactSurfaces(t, filepath.Join(offRoot, "summary"))
	onSnap := snapshotRecipeArtifactSurfaces(t, filepath.Join(onRoot, "summary"))
	// Require the durable artifact surfaces: records, descriptor, field catalog, provenance.
	for _, required := range []string{
		dataartifact.DescriptorFileName,
		provenance.ManifestFileName,
		"fields/records.fields.json",
	} {
		if _, ok := offSnap[required]; !ok {
			t.Fatalf("off run missing required surface %q (have %v)", required, keysSorted(offSnap))
		}
	}
	assertRecipeSurfacesEqual(t, offSnap, onSnap)

	// At least one records shard must be present and matched.
	hasRecords := false
	for k := range offSnap {
		if strings.HasPrefix(k, "records") && strings.HasSuffix(k, ".jsonl") {
			hasRecords = true
			break
		}
	}
	if !hasRecords {
		t.Fatal("expected records*.jsonl in recipe output")
	}

	if _, err := os.Stat(eventsPath); err != nil {
		t.Fatalf("events: %v", err)
	}
	arts := terminalArtifacts(t, eventsPath)
	if len(arts) != 1 {
		t.Fatalf("want bridge on process-run path, got %#v", arts)
	}
}

func TestExtractMultiArtifactBridge_MultiRecipeOrder(t *testing.T) {
	clearArtifactBridgeHooks(t)
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
			dA := readPublishedDescriptor(t, outRoot, "summary")
			dB := readPublishedDescriptor(t, outRoot, "line-items")
			if arts[0]["artifact_id"] != dA["artifact_id"] {
				t.Fatalf("first ref not summary: event=%v desc=%v", arts[0]["artifact_id"], dA["artifact_id"])
			}
			if arts[1]["artifact_id"] != dB["artifact_id"] {
				t.Fatalf("second ref not line-items: event=%v desc=%v", arts[1]["artifact_id"], dB["artifact_id"])
			}
			assertEventRedactionCorpus(t, eventsPath, "summary", "line-items", "s3://", outRoot)
		})
	}
}

func TestExtractMultiArtifactBridge_PartialIndependentClaims(t *testing.T) {
	clearArtifactBridgeHooks(t)
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
	if arts[0]["lifecycle"] != desc["lifecycle"] || desc["lifecycle"] != "partial" {
		t.Fatalf("lifecycle event=%v desc=%v", arts[0]["lifecycle"], desc["lifecycle"])
	}
}

func TestExtractMultiArtifactBridge_NoProcessRunNoBridgeWork(t *testing.T) {
	clearArtifactBridgeHooks(t)
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

func TestExtractMultiArtifactBridge_PublishFailureNoRef(t *testing.T) {
	clearArtifactBridgeHooks(t)
	dataArtifactDescriptorPublishHook = func(tgt *uriio.OutputTarget) error {
		return errors.New("injected descriptor publish failure")
	}
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	outRoot, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList:             fileList,
		ProcessRunEventsPath: eventsPath,
		ArtifactDescriptor:   true,
		ArtifactContractBase: dataArtifactContractBase(t),
	}, ws, io.Discard)
	if err == nil {
		t.Fatal("expected fatal error from descriptor publish failure")
	}
	if !strings.Contains(err.Error(), "injected descriptor publish failure") {
		t.Fatalf("error must preserve publish failure, got: %v", err)
	}
	assertSchemaValidStream(t, eventsPath)
	assertOneTerminalLast(t, readEventKinds(t, eventsPath), "failed")
	if arts := terminalArtifacts(t, eventsPath); arts != nil {
		t.Fatalf("unpublished descriptor must not bridge, got %#v", arts)
	}
	// Staged file may exist after local atomic write; receipt/bridge must not.
	_ = outRoot
}

func TestExtractMultiArtifactBridge_MixedRecipePublishSubset(t *testing.T) {
	clearArtifactBridgeHooks(t)
	// Fail only the second recipe's descriptor publish; first succeeds.
	dataArtifactDescriptorPublishHook = func(tgt *uriio.OutputTarget) error {
		if strings.Contains(tgt.LocalPath, string(filepath.Separator)+"line-items"+string(filepath.Separator)) ||
			strings.Contains(tgt.LogicalURI, "line-items") {
			return errors.New("injected sibling descriptor publish failure")
		}
		return nil
	}
	wsA := writeMultiRecipeWorkspace(t, "summary")
	wsB := writeMultiRecipeWorkspace(t, "line-items")
	fileList, _ := writeMultiInputSet(t, 1)
	outRoot := filepath.Join(t.TempDir(), "out")
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	err := runExtractMulti(&multiSharedOptions{
		FileList:             fileList,
		OutputPath:           outRoot,
		RunID:                testMultiRunID,
		OutputMode:           "aggregate",
		ProcessRunEventsPath: eventsPath,
		ArtifactDescriptor:   true,
		ArtifactContractBase: dataArtifactContractBase(t),
	}, []string{wsA, wsB}, io.Discard, time.Now())
	if err == nil {
		t.Fatal("expected fatal error from sibling descriptor publish")
	}
	if !strings.Contains(err.Error(), "injected sibling descriptor publish failure") {
		t.Fatalf("want sibling publish error, got: %v", err)
	}
	assertSchemaValidStream(t, eventsPath)
	assertOneTerminalLast(t, readEventKinds(t, eventsPath), "failed")
	arts := terminalArtifacts(t, eventsPath)
	if len(arts) != 1 {
		t.Fatalf("want only successful recipe ref, got %#v", arts)
	}
	dA := readPublishedDescriptor(t, outRoot, "summary")
	if arts[0]["artifact_id"] != dA["artifact_id"] {
		t.Fatalf("ref must match successful summary descriptor: event=%v desc=%v", arts[0]["artifact_id"], dA["artifact_id"])
	}
	// Failed sibling must not invent a placeholder ref.
	if arts[0]["lifecycle"] != dA["lifecycle"] {
		t.Fatalf("lifecycle mismatch event=%v desc=%v", arts[0]["lifecycle"], dA["lifecycle"])
	}
}

func TestExtractMultiArtifactBridge_PostPublishValidateKeepsRef(t *testing.T) {
	clearArtifactBridgeHooks(t)
	extractOutputValidateHook = func(opts *ExtractOptions, manifest provenance.Manifest) error {
		_ = opts
		_ = manifest
		return errors.New("injected post-publish validation failure")
	}
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	outRoot, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList:             fileList,
		ProcessRunEventsPath: eventsPath,
		ArtifactDescriptor:   true,
		ArtifactContractBase: dataArtifactContractBase(t),
	}, ws, io.Discard)
	if err == nil {
		t.Fatal("expected post-publish validation error")
	}
	if !strings.Contains(err.Error(), "injected post-publish validation failure") {
		t.Fatalf("error = %v", err)
	}
	assertSchemaValidStream(t, eventsPath)
	assertOneTerminalLast(t, readEventKinds(t, eventsPath), "failed")
	arts := terminalArtifacts(t, eventsPath)
	if len(arts) != 1 {
		t.Fatalf("already-published descriptor must remain on failed terminal, got %#v", arts)
	}
	desc := readPublishedDescriptor(t, outRoot, "summary")
	if arts[0]["artifact_id"] != desc["artifact_id"] || arts[0]["lifecycle"] != desc["lifecycle"] {
		t.Fatalf("bridge must match durable descriptor: event=%#v desc artifact_id=%v lifecycle=%v",
			arts[0], desc["artifact_id"], desc["lifecycle"])
	}
}

func TestExtractMultiArtifactBridge_CloudLocatorNotInStream(t *testing.T) {
	clearArtifactBridgeHooks(t)
	const (
		bucket   = "secret-bucket-acme-prod"
		prefix   = "client-site-north/job-42"
		cloudURI = "s3://" + bucket + "/" + prefix + "/artifact-descriptor.json"
		akid     = "AKIAIOSFODNN7EXAMPLE"
		secret   = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
		xpath    = "/root/TargetElement/Name"
	)
	// Inject a cloud-looking LogicalURI at the publication gate (equivalent to a
	// cloud target without requiring the s3integration harness).
	dataArtifactDescriptorPublishHook = func(tgt *uriio.OutputTarget) error {
		tgt.LogicalURI = cloudURI
		return nil
	}
	// Hostile path segments + secret-shaped inputs that must not appear in events.
	hostileRoot := filepath.Join(t.TempDir(), "out-"+bucket, prefix)
	ws := writeMultiRecipeWorkspace(t, "summary")
	dir := t.TempDir()
	// Seed record content with secret-shaped and xpath-like markers.
	input := filepath.Join(dir, "src-"+akid+".xml")
	body := `<root><TargetElement><Name>` + secret + xpath + `</Name></TargetElement></root>`
	if err := os.WriteFile(input, []byte(body), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	list := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(list, []byte(input+"\n"), 0o600); err != nil {
		t.Fatalf("write list: %v", err)
	}
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	err := runExtractMulti(&multiSharedOptions{
		FileList:             list,
		OutputPath:           hostileRoot,
		RunID:                testMultiRunID,
		OutputMode:           "aggregate",
		ProcessRunEventsPath: eventsPath,
		ArtifactDescriptor:   true,
		ArtifactContractBase: dataArtifactContractBase(t),
	}, []string{ws}, io.Discard, time.Now())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertSchemaValidStream(t, eventsPath)
	arts := terminalArtifacts(t, eventsPath)
	if len(arts) != 1 {
		t.Fatalf("want 1 ref, got %#v", arts)
	}
	assertEventRedactionCorpus(t, eventsPath,
		"s3://", bucket, prefix, cloudURI, akid, secret, xpath,
		"summary", "artifact-descriptor.json", hostileRoot, "TargetElement",
	)
}

func TestExtractMultiArtifactBridge_PanicAfterPublishKeepsRef(t *testing.T) {
	clearArtifactBridgeHooks(t)
	extractOutputValidateHook = func(opts *ExtractOptions, manifest provenance.Manifest) error {
		_ = opts
		_ = manifest
		panic("injected panic after descriptor publish")
	}
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	var runErr error
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to re-raise after terminal emission")
			}
		}()
		_, runErr = runMultiWithProcessRunOpts(t, &multiSharedOptions{
			FileList:             fileList,
			ProcessRunEventsPath: eventsPath,
			ArtifactDescriptor:   true,
			ArtifactContractBase: dataArtifactContractBase(t),
		}, ws, io.Discard)
	}()
	_ = runErr
	// Panic path records failed terminal with any already-published receipt.
	assertSchemaValidStream(t, eventsPath)
	assertOneTerminalLast(t, readEventKinds(t, eventsPath), "failed")
	arts := terminalArtifacts(t, eventsPath)
	if len(arts) != 1 {
		t.Fatalf("panic after publish must still list receipt, got %#v", arts)
	}
}

func TestExtractMultiArtifactBridge_TerminalWriteFailLeavesDescriptor(t *testing.T) {
	clearArtifactBridgeHooks(t)
	// Fail-open: mid-run stream writer failure withholds telemetry; published
	// descriptors remain. Use OpenWithWriter via processrun unit coverage for
	// Sync/Close; here prove durable descriptor bytes survive when events path
	// collides (fail-open disable before any bridge attach from a second open).
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	// Seed an existing stream so process-run opens fail-open (no clobber).
	if err := os.WriteFile(eventsPath, []byte("PRIOR\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	outRoot, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList:             fileList,
		ProcessRunEventsPath: eventsPath,
		ArtifactDescriptor:   true,
		ArtifactContractBase: dataArtifactContractBase(t),
	}, ws, io.Discard)
	if err != nil {
		t.Fatalf("extract must succeed when telemetry fails open: %v", err)
	}
	// Descriptor still published.
	descPath := filepath.Join(outRoot, "summary", dataartifact.DescriptorFileName)
	if _, err := os.Stat(descPath); err != nil {
		t.Fatalf("descriptor must remain after telemetry withhold: %v", err)
	}
	// Prior stream not clobbered / no bridge written into it.
	raw, _ := os.ReadFile(eventsPath) // #nosec G304
	if string(raw) != "PRIOR\n" {
		t.Fatalf("existing stream mutated: %q", raw)
	}
	if strings.Contains(string(raw), "artifacts") {
		t.Fatal("fail-open path must not publish bridge into withheld stream")
	}
}

func TestExtractMultiArtifactBridge_ContractBaseFailureNoRef(t *testing.T) {
	clearArtifactBridgeHooks(t)
	// Fail before Publish at contract resolution — no receipt.
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	_, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList:             fileList,
		ProcessRunEventsPath: eventsPath,
		ArtifactDescriptor:   true,
		ArtifactContractBase: filepath.Join(t.TempDir(), "missing-contract-base"),
	}, ws, io.Discard)
	if err == nil {
		t.Fatal("expected contract-base failure")
	}
	assertSchemaValidStream(t, eventsPath)
	assertOneTerminalLast(t, readEventKinds(t, eventsPath), "failed")
	if arts := terminalArtifacts(t, eventsPath); arts != nil {
		t.Fatalf("pre-publish failure must not bridge, got %#v", arts)
	}
}

// Ensure processrun.ArtifactRef still rejects cloud locators at the typed boundary.
func TestArtifactRefRejectsCloudLocator(t *testing.T) {
	id := "urn:uuid:018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	bad := processrun.ArtifactRef{
		ArtifactID: id,
		Lifecycle:  "complete",
		Descriptor: "s3://secret-bucket/prefix/artifact-descriptor.json",
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected cloud descriptor to fail Validate")
	}
}
