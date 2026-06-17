package uriio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gonimbusprovider "github.com/3leaps/gonimbus/pkg/provider"
)

// TestSessionStageDirSecure is S5: the run staging dir is created with an
// unpredictable name and owner-only permissions, and Close removes it.
func TestSessionStageDirSecure(t *testing.T) {
	workDir := t.TempDir()
	sess := NewSession(NewResolver(nil, nil), workDir, "run-abc-123")

	dir, err := sess.ensureStageDir()
	if err != nil {
		t.Fatalf("ensureStageDir: %v", err)
	}
	if filepath.Dir(dir) != filepath.Join(workDir, stagingDirName) {
		t.Errorf("stage dir %q not under work/cloud", dir)
	}
	// Lazy + memoized: a second call returns the same dir.
	dir2, _ := sess.ensureStageDir()
	if dir != dir2 {
		t.Errorf("ensureStageDir not memoized: %q vs %q", dir, dir2)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("stage dir mode = %#o, want 0700", perm)
		}
	}

	if err := sess.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("stage dir not removed on Close: %v", err)
	}
	// Close is idempotent.
	if err := sess.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestSweepStaleStaging removes run dirs older than maxAge and keeps fresh ones.
// TestSessionRejectsAnonymousWrite is PA1: an anonymous (read-only) handle is
// rejected on the output side both at the run-start describe and at open-output
// time, before any staging directory is created — Sumpter fails closed rather
// than relying on the provider's PutObject-time rejection.
func TestSessionRejectsAnonymousWrite(t *testing.T) {
	workDir := t.TempDir()
	cfg := &CredentialsConfig{Handles: map[string]HandleConfig{
		"public": {Anonymous: true, Region: "us-east-1"},
	}}
	sess := NewSession(NewResolver(cfg, nil), workDir, "run-anon")

	if _, err := sess.DescribeOutputHandle("public", "bucket"); err == nil || !strings.Contains(err.Error(), "anonymous") {
		t.Fatalf("DescribeOutputHandle(public) = %v, want anonymous-write rejection", err)
	}

	if _, err := sess.OpenOutput(context.Background(), "s3://bucket/out.json", "public"); err == nil || !strings.Contains(err.Error(), "anonymous") {
		t.Fatalf("OpenOutput(public) = %v, want anonymous-write rejection", err)
	}

	// The rejected write must not have created a staging directory.
	if _, err := os.Stat(filepath.Join(workDir, stagingDirName)); !os.IsNotExist(err) {
		t.Errorf("anonymous write created a staging directory (err=%v); PA1 must fail before staging", err)
	}
}

// TestCloudOpError covers the shared cloud read/write error seam (PR-8): every
// cloud-op error is redacted (literal secret + AKID pattern) before surfacing, and
// a throttle / temporarily-unavailable condition is labeled transient while other
// errors are not.
func TestCloudOpError(t *testing.T) {
	// Redaction: a literal secret and an AKID-shaped token are both scrubbed, and a
	// non-transient error is not mislabeled.
	raw := errors.New("PutObject denied for AKIAIOSFODNN7EXAMPLE using secret literal-secret-value")
	got := cloudOpError(raw, []string{"literal-secret-value"})
	if strings.Contains(got, "literal-secret-value") {
		t.Errorf("cloudOpError leaked the literal secret: %q", got)
	}
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("cloudOpError leaked the AKID: %q", got)
	}
	if strings.Contains(got, "transient") {
		t.Errorf("non-transient error wrongly labeled transient: %q", got)
	}

	// Classification: throttle and temporarily-unavailable are labeled transient.
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"throttled", fmt.Errorf("s3 PutObject: %w", gonimbusprovider.ErrThrottled)},
		{"unavailable", fmt.Errorf("s3 GetObject: %w", gonimbusprovider.ErrProviderUnavailable)},
	} {
		if msg := cloudOpError(tc.err, nil); !strings.Contains(msg, "transient") {
			t.Errorf("%s error not labeled transient: %q", tc.name, msg)
		}
	}
}

func TestSweepStaleStaging(t *testing.T) {
	workDir := t.TempDir()
	base := filepath.Join(workDir, stagingDirName)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(base, "stale-run")
	fresh := filepath.Join(base, "fresh-run")
	for _, d := range []string{stale, fresh} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	SweepStaleStaging(workDir, 24*time.Hour, now)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale run dir not swept: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh run dir wrongly swept: %v", err)
	}
}

// TestEndpointPostureAllowlist is S6: only explicit https:// (or insecure opt-in)
// is accepted at construction; scheme-less and http:// are rejected.
func TestEndpointPostureAllowlist(t *testing.T) {
	cases := []struct {
		name    string
		hc      HandleConfig
		wantErr bool
	}{
		{"empty endpoint (AWS default)", HandleConfig{}, false},
		{"https endpoint", HandleConfig{Endpoint: "https://s3.example.com"}, false},
		{"http endpoint rejected", HandleConfig{Endpoint: "http://minio:9000"}, true},
		{"scheme-less rejected", HandleConfig{Endpoint: "minio.local:9000"}, true},
		{"http with insecure opt-in", HandleConfig{Endpoint: "http://minio:9000", Insecure: true}, false},
		{"scheme-less with insecure opt-in", HandleConfig{Endpoint: "minio.local:9000", Insecure: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Direct gate assertion: validateEndpointPosture is the control, and it
			// is pure (no I/O), so for accepted postures we assert it returns nil
			// rather than ignoring all errors at construction. This fails if a
			// future change wrongly rejects https:// or an explicit insecure opt-in.
			gateErr := validateEndpointPosture("h", tc.hc)
			if tc.wantErr && gateErr == nil {
				t.Fatalf("validateEndpointPosture(%+v) = nil, want rejection", tc.hc)
			}
			if !tc.wantErr && gateErr != nil {
				t.Fatalf("validateEndpointPosture(%+v) = %v, want accepted", tc.hc, gateErr)
			}

			// End-to-end: the gate must also fire at provider construction (the
			// connect boundary), covering handles defined via CLI override.
			cfg := &CredentialsConfig{Handles: map[string]HandleConfig{"h": tc.hc}}
			pool := NewProviderPool(NewResolver(cfg, nil))
			t.Cleanup(func() { _ = pool.Close() })
			_, err := pool.Provider(context.Background(), "h", "bucket")
			gotErr := err != nil
			// For accepted postures, construction may still succeed offline; we
			// only assert the posture gate fires (or not) — distinguish the gate
			// error from any SDK error by message.
			if tc.wantErr && !gotErr {
				t.Fatalf("expected endpoint posture rejection for %+v", tc.hc)
			}
			if tc.wantErr && gotErr && !strings.Contains(err.Error(), "https://") {
				t.Fatalf("rejection not from endpoint posture: %v", err)
			}
		})
	}
}

// TestCloudIncludes covers the include-glob derivation for cloud listings.
func TestCloudIncludes(t *testing.T) {
	patternRef := Ref{Scheme: SchemeS3, Key: "data/", Pattern: "data/**/*.xml"}
	if got := cloudIncludes(patternRef, "data/", ""); got[0] != "**/*.xml" {
		t.Errorf("pattern include = %v, want [**/*.xml]", got)
	}
	prefixRef := Ref{Scheme: SchemeS3, Key: "data/"}
	if got := cloudIncludes(prefixRef, "data/", "*.xml"); got[0] != "*.xml" {
		t.Errorf("explicit include = %v, want [*.xml]", got)
	}
	if got := cloudIncludes(prefixRef, "data/", ""); got[0] != "**" {
		t.Errorf("default include = %v, want [**]", got)
	}
}

// TestSessionAcquireLocalPassthrough confirms local refs need no staging.
func TestSessionAcquireLocalPassthrough(t *testing.T) {
	sess := NewSession(NewResolver(nil, nil), t.TempDir(), "run")
	t.Cleanup(func() { _ = sess.Close() })
	src, err := sess.Acquire(context.Background(), "/local/path.xml", "")
	if err != nil {
		t.Fatalf("Acquire(local): %v", err)
	}
	if src.LocalPath != "/local/path.xml" || src.Scheme != SchemeLocal {
		t.Errorf("local acquire = %+v", src)
	}
}

// TestSessionAcquireRejectsPrefix confirms a prefix/pattern cannot be acquired
// as a single object.
func TestSessionAcquireRejectsPrefix(t *testing.T) {
	sess := NewSession(NewResolver(nil, nil), t.TempDir(), "run")
	t.Cleanup(func() { _ = sess.Close() })
	if _, err := sess.Acquire(context.Background(), "s3://bucket/prefix/", ""); err == nil {
		t.Error("Acquire(prefix) should error")
	}
}
