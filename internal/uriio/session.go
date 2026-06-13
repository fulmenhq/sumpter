package uriio

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gonimbusmatch "github.com/3leaps/gonimbus/pkg/match"
	gonimbusprovider "github.com/3leaps/gonimbus/pkg/provider"
)

// stagingDirName is the subdirectory of the work directory under which cloud
// source objects are materialized for a run.
const stagingDirName = "cloud"

// Session carries the per-run cloud state: the provider pool and a lazily
// created, run-scoped staging directory. Local references need no session state;
// cloud references are acquired through it so the staged files share one
// lifecycle and one cleanup.
type Session struct {
	pool    *ProviderPool
	workDir string
	runID   string

	mu       sync.Mutex
	stageDir string // created on first cloud acquire
}

// NewSession builds a run session over a resolver. workDir is the resolved
// Sumpter work directory; runID scopes the staging directory for this run.
func NewSession(resolver *Resolver, workDir, runID string) *Session {
	return &Session{
		pool:    NewProviderPool(resolver),
		workDir: workDir,
		runID:   runID,
	}
}

// Close removes the run's staged files and releases pooled providers. It is
// idempotent and safe to call on every exit path.
func (s *Session) Close() error {
	s.mu.Lock()
	stageDir := s.stageDir
	s.stageDir = ""
	s.mu.Unlock()

	var firstErr error
	if stageDir != "" {
		if err := os.RemoveAll(stageDir); err != nil {
			firstErr = fmt.Errorf("uriio: remove staging dir: %w", err)
		}
	}
	if err := s.pool.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// safeRunPrefix returns a filesystem-safe staging-dir prefix derived from runID.
func safeRunPrefix(runID string) string {
	var b strings.Builder
	for _, r := range runID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	prefix := b.String()
	if prefix == "" {
		prefix = "run"
	}
	return prefix + "-"
}

// ensureStageDir lazily creates the run-scoped staging directory with an
// unpredictable name and owner-only permissions, asserting the mode after
// creation rather than trusting umask.
func (s *Session) ensureStageDir() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stageDir != "" {
		return s.stageDir, nil
	}
	base := filepath.Join(s.workDir, stagingDirName)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("uriio: create cloud staging base: %w", err)
	}
	dir, err := os.MkdirTemp(base, safeRunPrefix(s.runID))
	if err != nil {
		return "", fmt.Errorf("uriio: create run staging dir: %w", err)
	}
	// A staging directory needs the owner execute bit to be traversable; 0700
	// (owner-only rwx) is the secure minimum and is asserted here rather than
	// trusting umask. #nosec G302 - 0700 is correct for a directory, not a file.
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302
		return "", fmt.Errorf("uriio: secure staging dir: %w", err)
	}
	s.stageDir = dir
	return dir, nil
}

// Acquire resolves a source reference to a local file. Local references pass
// through to their own path (no staging); s3:// references are fetched via the
// pooled provider and staged to a path-traversal-safe location under the run
// staging directory. The returned source carries the logical URI for provenance
// and the local staged path for the extraction core.
func (s *Session) Acquire(ctx context.Context, reference, handle string) (*AcquiredSource, error) {
	ref, err := Classify(reference)
	if err != nil {
		return nil, err
	}
	switch ref.Scheme {
	case SchemeLocal:
		return &AcquiredSource{LogicalURI: ref.LogicalURI, LocalPath: ref.LocalPath, Scheme: SchemeLocal}, nil
	case SchemeS3:
		return s.acquireS3(ctx, ref, handle)
	default:
		return nil, notImplemented("source acquisition", ref)
	}
}

func (s *Session) acquireS3(ctx context.Context, ref Ref, handle string) (*AcquiredSource, error) {
	if ref.IsPattern() || ref.IsPrefix() {
		return nil, fmt.Errorf("uriio: acquire needs a single object, not a prefix/pattern (%s) — list it first", ref.LogicalURI)
	}
	stageDir, err := s.ensureStageDir()
	if err != nil {
		return nil, err
	}
	staged, err := safeStagePath(stageDir, ref.Key)
	if err != nil {
		return nil, err
	}
	prov, err := s.pool.Provider(ctx, handle, ref.Bucket)
	if err != nil {
		return nil, err
	}
	body, _, err := prov.GetObject(ctx, ref.Key)
	if err != nil {
		return nil, fmt.Errorf("uriio: get %s: %w", ref.LogicalURI, err)
	}
	defer func() { _ = body.Close() }()

	if err := writeStagedFile(staged, body); err != nil {
		return nil, err
	}
	stagedPath := staged
	return &AcquiredSource{
		LogicalURI: ref.LogicalURI,
		LocalPath:  staged,
		Scheme:     SchemeS3,
		cleanup:    func() error { return os.Remove(stagedPath) },
	}, nil
}

// outputStagingDirName is the subdirectory of the run staging directory under
// which output artifacts are formed locally before publication, kept separate
// from acquired input objects so an output key can never collide with a source.
const outputStagingDirName = "out"

// maxSinglePutBytes is the S3 single-PUT object-size limit (5 GiB). gonimbus
// v0.2.3 publishes via a single PutObject (no multipart), so a larger object
// would fail in the SDK with a cryptic EntityTooLarge. We pre-check and fail
// clearly instead; large/multipart cloud output is a follow-on (SUM-006).
const maxSinglePutBytes = 5 * 1024 * 1024 * 1024

// DescribeOutputHandle validates that an output handle resolves (an unknown
// non-default handle errors here, before any write) and returns a redacted,
// log-safe description of the resolved destination: bucket, endpoint, region, and
// handle name only — never credentials. Used for the cloud-output run-start
// confirmation so a misroute is visible before bytes leave.
func (s *Session) DescribeOutputHandle(handle, bucket string) (string, error) {
	hc, err := s.pool.resolver.Resolve(handle)
	if err != nil {
		return "", err
	}
	endpoint := hc.Endpoint
	if endpoint == "" {
		endpoint = "(aws default)"
	}
	region := hc.Region
	if region == "" {
		region = "(default)"
	}
	return fmt.Sprintf("bucket=%s endpoint=%s region=%s handle=%s", bucket, endpoint, region, handle), nil
}

// OpenOutput resolves a destination for writing. Local destinations write
// directly to their final path with a no-op Publish (byte-for-byte the historical
// behavior). An s3:// destination writes to a staging file under the run
// directory; Publish uploads the fully-formed local artifact via a single
// PutObject (so a failed publish leaves no partial object) and then removes the
// staging file. The output handle may differ from the input handle — the provider
// pool is keyed per handle, so one session serves cloud->cloud with independent
// credentials.
func (s *Session) OpenOutput(ctx context.Context, reference, handle string) (*OutputTarget, error) {
	ref, err := Classify(reference)
	if err != nil {
		return nil, err
	}
	switch ref.Scheme {
	case SchemeLocal:
		return &OutputTarget{LogicalURI: ref.LogicalURI, LocalPath: ref.LocalPath, Scheme: SchemeLocal}, nil
	case SchemeS3:
		return s.openOutputS3(ref, handle)
	default:
		return nil, notImplemented("result publication", ref)
	}
}

func (s *Session) openOutputS3(ref Ref, handle string) (*OutputTarget, error) {
	if ref.IsPattern() || ref.IsPrefix() {
		return nil, fmt.Errorf("uriio: output needs a single object key, not a prefix/pattern (%s)", ref.LogicalURI)
	}
	stageDir, err := s.ensureStageDir()
	if err != nil {
		return nil, err
	}
	staged, err := safeStagePath(filepath.Join(stageDir, outputStagingDirName), ref.Key)
	if err != nil {
		return nil, err
	}
	bucket, key, logical := ref.Bucket, ref.Key, ref.LogicalURI
	return &OutputTarget{
		LogicalURI: logical,
		LocalPath:  staged,
		Scheme:     SchemeS3,
		publish: func(ctx context.Context) error {
			prov, err := s.pool.Provider(ctx, handle, bucket)
			if err != nil {
				return err
			}
			// #nosec G304 - staged is validated by safeStagePath to stay under the run dir.
			f, err := os.Open(staged)
			if err != nil {
				return fmt.Errorf("uriio: open staged output: %w", err)
			}
			defer func() { _ = f.Close() }()
			info, err := f.Stat()
			if err != nil {
				return fmt.Errorf("uriio: stat staged output: %w", err)
			}
			if err := publishSizeWithinLimit(logical, info.Size()); err != nil {
				return err
			}
			// PutObject is a single PUT (no multipart in this provider): it either
			// stores the whole object or fails, so a publish error never leaves a
			// truncated object a consumer could read as complete.
			if err := prov.PutObject(ctx, key, f, info.Size()); err != nil {
				// Surface only the logical destination + a redacted reason: the raw
				// AWS SDK error can echo credential material (e.g. an access key id on
				// an auth failure). Use %s, not %w, so the unredacted SDK error cannot
				// be re-exposed by downstream error wrapping. Redaction additionally
				// scrubs any literal-key cleartext for this handle.
				return fmt.Errorf("uriio: publish %s failed: %s", logical, redactSecrets(err.Error(), s.pool.redactionSecrets(handle)))
			}
			// Best-effort: the run staging dir is removed wholesale by Session.Close,
			// so a failure to remove the individual staged file here is not fatal.
			_ = os.Remove(staged)
			return nil
		},
	}, nil
}

// publishSizeWithinLimit fails clearly when a cloud output object would exceed
// the single-PUT ceiling, instead of letting the SDK return a cryptic
// EntityTooLarge. Large/multipart cloud output is a follow-on (SUM-006).
func publishSizeWithinLimit(logical string, size int64) error {
	if size > maxSinglePutBytes {
		return fmt.Errorf("uriio: cloud output object %s is %d bytes, exceeding the %d-byte single-PUT limit; large/multipart cloud output is deferred (SUM-006)", logical, size, int64(maxSinglePutBytes))
	}
	return nil
}

// redactSecrets replaces any occurrence of the given secret cleartext values in
// s with a placeholder. It is applied to provider/SDK error strings before they
// are surfaced to logs, stderr, or persisted artifacts, so credential material
// (e.g. a literal access key id echoed in an auth error) never leaks. Handles
// that resolve credentials via an AWS profile expose no cleartext here, so their
// secrets[] is empty — the belt-and-suspenders control there is that publish
// errors surface only the logical destination, never the raw SDK message chain.
func redactSecrets(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		s = strings.ReplaceAll(s, sec, "[redacted]")
	}
	return s
}

// writeStagedFile writes r to path, creating parents and refusing to follow a
// pre-planted symlink (O_EXCL).
func writeStagedFile(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("uriio: create staging subdir: %w", err)
	}
	// #nosec G304 - path is validated by safeStagePath to stay under the run dir.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("uriio: create staged file: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("uriio: stage object: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("uriio: finalize staged file: %w", err)
	}
	return nil
}

// List enumerates source objects for a reference. Cloud prefixes/patterns are
// listed via the pooled provider and filtered with include/exclude globs
// (matched against the key relative to the listing prefix). A full-bucket
// (empty-prefix) listing is reported via Listing.FullBucketScan so the caller can
// warn or gate it.
func (s *Session) List(ctx context.Context, reference, handle, include, exclude string) (*Listing, error) {
	ref, err := Classify(reference)
	if err != nil {
		return nil, err
	}
	if ref.Scheme != SchemeS3 {
		return nil, fmt.Errorf("uriio: session List is for cloud references; local discovery is handled by the caller")
	}
	return s.listS3(ctx, ref, handle, include, exclude)
}

func (s *Session) listS3(ctx context.Context, ref Ref, handle, include, exclude string) (*Listing, error) {
	prov, err := s.pool.Provider(ctx, handle, ref.Bucket)
	if err != nil {
		return nil, err
	}

	prefix := ref.Key
	includes := cloudIncludes(ref, prefix, include)
	matchCfg := gonimbusmatch.Config{Includes: includes}
	if strings.TrimSpace(exclude) != "" {
		matchCfg.Excludes = []string{exclude}
	}
	matcher, err := gonimbusmatch.New(matchCfg)
	if err != nil {
		return nil, fmt.Errorf("uriio: build cloud matcher: %w", err)
	}

	listing := &Listing{Scheme: SchemeS3, FullBucketScan: prefix == ""}
	token := ""
	for {
		res, listErr := prov.List(ctx, gonimbusprovider.ListOptions{Prefix: prefix, ContinuationToken: token})
		if listErr != nil {
			return nil, fmt.Errorf("uriio: list %s: %w", ref.LogicalURI, listErr)
		}
		for _, obj := range res.Objects {
			if strings.HasSuffix(obj.Key, "/") {
				continue // skip directory placeholder keys
			}
			rel := strings.TrimPrefix(obj.Key, prefix)
			if !matcher.Match(rel) {
				continue
			}
			listing.Entries = append(listing.Entries, ListEntry{
				LogicalURI: (&objectURIRef{bucket: ref.Bucket, key: obj.Key}).String(),
				Size:       obj.Size,
			})
		}
		if !res.IsTruncated || res.ContinuationToken == "" {
			break
		}
		token = res.ContinuationToken
	}
	return listing, nil
}

// cloudIncludes derives the include globs for a cloud listing: the URI's own
// glob tail when present, otherwise the caller's include pattern, otherwise a
// match-everything pattern.
func cloudIncludes(ref Ref, prefix, include string) []string {
	if ref.IsPattern() {
		tail := strings.TrimPrefix(ref.Pattern, prefix)
		if tail != "" {
			return []string{tail}
		}
	}
	if strings.TrimSpace(include) != "" {
		return []string{include}
	}
	return []string{"**"}
}

// objectURIRef renders a canonical s3:// URI for a bucket/key.
type objectURIRef struct {
	bucket string
	key    string
}

func (o *objectURIRef) String() string { return "s3://" + o.bucket + "/" + o.key }

// SweepStaleStaging removes run staging directories older than maxAge under the
// work directory. Cleanup on success/handled-failure removes the run dir, but a
// SIGKILL/OOM/panic can leave staged cloud source data behind; this startup
// sweep bounds that exposure.
//
// Concurrency contract: this is a STARTUP orphan sweep, not a periodic GC. Call
// it once before a run begins staging, with a conservative maxAge (the caller
// uses a full day). It keys off each run dir's mod time, so a still-live
// concurrent run is safe only because maxAge far exceeds any real run's duration
// — do NOT call it mid-run or shorten maxAge toward a plausible run length, or it
// could delete another process's active staging. It is best-effort: individual
// removal failures are ignored so a busy concurrent run is never disturbed.
func SweepStaleStaging(workDir string, maxAge time.Duration, now time.Time) {
	base := filepath.Join(workDir, stagingDirName)
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.RemoveAll(filepath.Join(base, e.Name()))
		}
	}
}
