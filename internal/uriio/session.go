package uriio

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	gonimbusmatch "github.com/3leaps/gonimbus/pkg/match"
	gonimbusprovider "github.com/3leaps/gonimbus/pkg/provider"
	gonimbuss3 "github.com/3leaps/gonimbus/pkg/provider/s3"
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
	budget  *StagingBudget

	mu       sync.Mutex
	stageDir string // created on first cloud acquire
}

// SetStagingBudget attaches a run-global staging cap used by bounded acquire.
func (s *Session) SetStagingBudget(b *StagingBudget) {
	if s != nil {
		s.budget = b
	}
}

// StagingSnapshot returns aggregate-only staging stats (never URIs).
func (s *Session) StagingSnapshot() StagingStats {
	if s == nil {
		return StagingStats{}
	}
	return s.budget.Stats()
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
		return s.acquireS3(ctx, ref, handle, 0)
	default:
		return nil, notImplemented("source acquisition", ref)
	}
}

// AcquireBounded is Acquire with a pre-read size cap (bytes). For an s3:// source it
// rejects an oversized object using the object-size metadata BEFORE staging, and
// bounds the staged write with a limit reader as defence-in-depth against a
// mis-reported size — so an oversized object can never fill the staging disk first
// (the staging-disk DoS guard). maxBytes <= 0 means unbounded (identical to Acquire).
// Local sources pass through unstaged; their cap is enforced by the reader that
// consumes them.
func (s *Session) AcquireBounded(ctx context.Context, reference, handle string, maxBytes int64) (*AcquiredSource, error) {
	ref, err := Classify(reference)
	if err != nil {
		return nil, err
	}
	switch ref.Scheme {
	case SchemeLocal:
		return &AcquiredSource{LogicalURI: ref.LogicalURI, LocalPath: ref.LocalPath, Scheme: SchemeLocal}, nil
	case SchemeS3:
		return s.acquireS3(ctx, ref, handle, maxBytes)
	default:
		return nil, notImplemented("source acquisition", ref)
	}
}

func (s *Session) acquireS3(ctx context.Context, ref Ref, handle string, maxBytes int64) (*AcquiredSource, error) {
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
	secrets := s.pool.redactionSecrets(handle)
	if s.budget != nil {
		return s.acquireS3Bounded(ctx, prov, ref, secrets, staged, maxBytes)
	}

	body, size, err := prov.GetObject(ctx, ref.Key)
	if err != nil {
		// Redact + classify the cloud read error: the raw SDK error can echo
		// credential material, and a throttle/unavailable condition is labeled so
		// the operator knows it is transient. %s (not %w) so the raw error cannot be
		// re-exposed by downstream wrapping.
		return nil, fmt.Errorf("uriio: get %s failed: %s", ref.LogicalURI, cloudOpError(err, secrets))
	}
	defer func() { _ = body.Close() }()

	// C2 staging-disk DoS guard: when a cap is set, reject an oversized object using
	// the object-size metadata before writing a single byte, and bound the staged
	// write so a mis-reported size still cannot overrun the cap.
	reader := io.Reader(body)
	if maxBytes > 0 {
		if size > maxBytes {
			return nil, fmt.Errorf("uriio: object %s is %d bytes, exceeding the %d-byte cap; not staged", ref.LogicalURI, size, maxBytes)
		}
		reader = io.LimitReader(body, maxBytes+1)
	}

	if err := writeStagedFile(staged, reader); err != nil {
		return nil, err
	}
	if maxBytes > 0 {
		if fi, statErr := os.Stat(staged); statErr == nil && fi.Size() > maxBytes {
			_ = os.Remove(staged)
			return nil, fmt.Errorf("uriio: object %s exceeded the %d-byte cap during staging; removed", ref.LogicalURI, maxBytes)
		}
	}
	stagedPath := staged
	return &AcquiredSource{
		LogicalURI: ref.LogicalURI,
		LocalPath:  staged,
		Scheme:     SchemeS3,
		cleanup:    func() error { return os.Remove(stagedPath) },
	}, nil
}

func (s *Session) acquireS3Bounded(ctx context.Context, prov *gonimbuss3.Provider, ref Ref, secrets []string, staged string, maxBytes int64) (*AcquiredSource, error) {
	var meta *gonimbusprovider.ObjectMeta
	if herr := s.retryTransient(ctx, func() error {
		var e error
		meta, e = prov.Head(ctx, ref.Key)
		return e
	}); herr != nil {
		return nil, fmt.Errorf("uriio: head %s failed: %s", ref.LogicalURI, cloudOpError(herr, secrets))
	}
	size := meta.Size
	if maxBytes > 0 && size > maxBytes {
		return nil, fmt.Errorf("uriio: object %s is %d bytes, exceeding the %d-byte cap; not staged", ref.LogicalURI, size, maxBytes)
	}
	if err := s.budget.Admit(ctx, size); err != nil {
		return nil, err
	}
	admitted := true
	releaseAdmit := func() {
		if admitted {
			s.budget.Release(size)
			admitted = false
		}
	}

	var body io.ReadCloser
	if gerr := s.retryTransient(ctx, func() error {
		var e error
		var got int64
		body, got, e = prov.GetObject(ctx, ref.Key)
		if e != nil {
			return e
		}
		if maxBytes > 0 && got > maxBytes {
			_ = body.Close()
			body = nil
			return fmt.Errorf("uriio: object %s is %d bytes, exceeding the %d-byte cap; not staged", ref.LogicalURI, got, maxBytes)
		}
		if err := StagingGetSizeMismatch(size, got); err != nil {
			_ = body.Close()
			body = nil
			return err
		}
		return nil
	}); gerr != nil {
		releaseAdmit()
		if body != nil {
			_ = body.Close()
		}
		return nil, fmt.Errorf("uriio: get %s failed: %s", ref.LogicalURI, cloudOpError(gerr, secrets))
	}
	defer func() { _ = body.Close() }()

	// Bound the write to admitted+1 so a longer stream cannot silently inflate
	// the run-global byte reservation; then compare the on-disk size.
	reader := io.LimitReader(body, size+1)
	if err := writeStagedFile(staged, reader); err != nil {
		releaseAdmit()
		return nil, err
	}
	if err := StagedBytesExceedAdmit(staged, size); err != nil {
		_ = os.Remove(staged)
		releaseAdmit()
		return nil, err
	}
	stagedPath := staged
	budget := s.budget
	return &AcquiredSource{
		LogicalURI: ref.LogicalURI,
		LocalPath:  staged,
		Scheme:     SchemeS3,
		cleanup: func() error {
			rmErr := os.Remove(stagedPath)
			if rmErr != nil {
				if budget != nil {
					budget.NoteCleanupFailure()
				}
				return rmErr
			}
			releaseAdmit()
			return nil
		},
	}, nil
}

// retryTransient retries classified throttle/unavailable errors with a small
// cap. Credential, access-denied, and not-implemented errors fail immediately.
func (s *Session) retryTransient(ctx context.Context, op func() error) error {
	var last error
	for attempt := 0; attempt <= acquireRetryLimit; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = op()
		if last == nil {
			return nil
		}
		throttled := gonimbusprovider.IsThrottled(last)
		unavail := gonimbusprovider.IsProviderUnavailable(last)
		if !throttled && !unavail {
			return last
		}
		if attempt == acquireRetryLimit {
			return last
		}
		if s.budget != nil {
			s.budget.NoteRetry(throttled)
		}
		timer := time.NewTimer(acquireBackoff(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return last
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

// MaxSinglePutBytes is the S3 single-PUT object-size limit (5 GiB), exported so
// callers (e.g. aggregate output) can enforce it PROACTIVELY at plan time — rolling
// or rejecting before gigabytes are streamed to a staging file — rather than only at
// publish via publishSizeWithinLimit.
const MaxSinglePutBytes = maxSinglePutBytes

// DescribeOutputHandle validates that an output handle resolves (an unknown
// non-default handle errors here, before any write) and returns a redacted,
// log-safe description of the resolved destination: bucket, endpoint, region, and
// handle name only — never credentials. Used for the cloud-output run-start
// confirmation so a misroute is visible before bytes leave.
func (s *Session) DescribeOutputHandle(handle, bucket string) (string, error) {
	// PA1: reject an anonymous (read-only) handle on the output side at run start,
	// before any extraction or staging. The openOutputS3 gate is the backstop.
	if err := s.pool.resolver.rejectAnonymousWrite(handle); err != nil {
		return "", err
	}
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
	// PA1 (authoritative backstop): never stage an output under an anonymous
	// (read-only) handle. This gate covers every cloud write path — result,
	// provenance sidecar, parquet — since they all resolve their destination here,
	// and it fires before any staging directory or file is created.
	if err := s.pool.resolver.rejectAnonymousWrite(handle); err != nil {
		return nil, err
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
				// Surface only the logical destination + a redacted, classified reason
				// via the shared cloud-op seam: the raw AWS SDK error can echo
				// credential material (e.g. an access key id on an auth failure), and a
				// throttle/unavailable publish failure is labeled transient. %s (not %w)
				// so the unredacted SDK error cannot be re-exposed by downstream
				// wrapping. The failure stays fatal (publish-fatal, S9) regardless of the
				// classification.
				return fmt.Errorf("uriio: publish %s failed: %s", logical, cloudOpError(err, s.pool.redactionSecrets(handle)))
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
// s with a placeholder, then applies a last-defense scrub of AWS access-key-id-
// shaped tokens. It is applied to provider/SDK error strings before they are
// surfaced to logs, stderr, or persisted artifacts, so credential material never
// leaks. The literal-value pass covers config-supplied keys (S1); the pattern
// pass covers profile/default-chain handles, whose cleartext sumpter never holds
// — so if the SDK echoes an AKID on an auth failure there is otherwise nothing to
// scrub it.
func redactSecrets(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		s = strings.ReplaceAll(s, sec, "[redacted]")
	}
	return redactAWSKeyIDs(s)
}

// cloudOpError builds a safe, operator-facing message for a failed cloud provider
// operation (GetObject / List / PutObject). It is the single redaction+classify
// seam for every cloud read and write boundary, so no cloud-op error reaches a
// log, stderr, or persisted artifact raw.
//
//   - Classify first: a transient condition (the object store throttled the
//     request or was temporarily unavailable, via the gonimbus provider sentinels)
//     is labeled. Sumpter runs no retry layer of its own — the AWS SDK retries
//     throttle/5xx internally — so a transient error that surfaces here means
//     those retries are already exhausted; the label tells the operator a later
//     re-run may succeed (vs. a permanent config/auth error they must fix). The
//     failure stays fatal either way; classification never changes disposition.
//   - Redact second: scrub any literal secret and AWS key-id-shaped token, because
//     an SDK error can echo credential material on an auth/throttle failure.
//
// The caller renders the result with %s (never %w), so the raw SDK error cannot be
// re-exposed unredacted by downstream wrapping. Classification is read from the raw
// err before this flattening.
func cloudOpError(err error, secrets []string) string {
	msg := redactSecrets(err.Error(), secrets)
	if gonimbusprovider.IsThrottled(err) || gonimbusprovider.IsProviderUnavailable(err) {
		msg += " [transient: the object store throttled the request or was temporarily unavailable; a later retry may succeed]"
	}
	return msg
}

// awsAccessKeyIDInText matches an AWS access key id embedded in a larger string:
// a 4-letter principal-type prefix (AKIA long-term, ASIA temporary/STS, AROA
// role, AIDA user, etc.) plus 16 uppercase-alphanumeric characters. Unanchored,
// unlike the validation pattern in cli.go which matches a whole token.
var awsAccessKeyIDInText = regexp.MustCompile(`(AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASCA)[A-Z0-9]{16}`)

// redactAWSKeyIDs scrubs AWS access-key-id-shaped tokens from a string. This is
// the last-defense control for handles whose secret cleartext sumpter does not
// hold (profile / default-chain), where an SDK error could echo the resolved AKID.
func redactAWSKeyIDs(s string) string {
	return awsAccessKeyIDInText.ReplaceAllString(s, "[redacted-key-id]")
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
			// Same redact + classify seam as the read/write boundaries: the cloud
			// list path is reachable on prefix/parallel (record-index) reads and must
			// not surface a raw SDK error. %s (not %w) for the same reason.
			return nil, fmt.Errorf("uriio: list %s failed: %s", ref.LogicalURI, cloudOpError(listErr, s.pool.redactionSecrets(handle)))
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
