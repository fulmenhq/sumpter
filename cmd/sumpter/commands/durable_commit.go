package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const aggregateCommitLockName = ".sumpter-aggregate.lock"

// durableCommitOps is the narrow filesystem seam used by the local aggregate
// commit protocol. Tests replace individual operations or inject named step
// failures; production uses the OS implementation below.
type durableCommitOps struct {
	openFile   func(string, int, fs.FileMode) (*os.File, error)
	createTemp func(string, string) (*os.File, error)
	rename     func(string, string) error
	remove     func(string) error
	stat       func(string) (fs.FileInfo, error)
	mkdir      func(string, fs.FileMode) error
	mkdirAll   func(string, fs.FileMode) error
	open       func(string) (*os.File, error)
	readDir    func(string) ([]os.DirEntry, error)
	syncData   func(*os.File) error
	syncDir    func(*os.File) error
	closeFile  func(*os.File) error
	step       func(string) error
}

func osDurableCommitOps() durableCommitOps {
	return durableCommitOps{
		openFile:   os.OpenFile,
		createTemp: os.CreateTemp,
		rename:     os.Rename,
		remove:     os.Remove,
		stat:       os.Stat,
		mkdir:      os.Mkdir,
		mkdirAll:   os.MkdirAll,
		open:       os.Open,
		readDir:    os.ReadDir,
		syncData:   platformSyncData,
		syncDir:    platformSyncDirectory,
		closeFile:  func(f *os.File) error { return f.Close() },
	}
}

func (o durableCommitOps) before(step string) error {
	if o.step == nil {
		return nil
	}
	return o.step(step)
}

// localAggregateCommit owns one recipe output directory from the first local
// aggregate mutation through the final durable manifest marker. It never removes
// paths it did not create and track itself.
type localAggregateCommit struct {
	outputDir string
	durable   bool
	ops       durableCommitOps

	prepared      bool
	committed     bool
	lockPath      string
	lockHeld      bool
	markerRenamed bool
	stagePaths    []string
	finalPaths    []string
	sidecarPaths  []string
	tempPaths     []string
	createdDirs   []string
}

func newLocalAggregateCommit(opts *ExtractOptions) *localAggregateCommit {
	ops := osDurableCommitOps()
	if opts != nil && opts.durableCommitOps != nil {
		ops = *opts.durableCommitOps
	}
	return &localAggregateCommit{
		outputDir: filepath.Clean(opts.OutputPath),
		durable:   !opts.NoDurableCommit,
		ops:       ops,
	}
}

func (c *localAggregateCommit) prepare() error {
	if c == nil || c.prepared {
		return nil
	}
	if err := validateLocalDurableCommitSupport(c.outputDir); err != nil {
		return err
	}
	if c.durable {
		if err := c.ensureDirectoryDurable(c.outputDir); err != nil {
			return errors.Join(err, c.cleanupCreatedDirectories())
		}
	} else if err := c.ops.mkdirAll(c.outputDir, 0o750); err != nil {
		return fmt.Errorf("create aggregate output directory: %w", err)
	}

	c.lockPath = filepath.Join(c.outputDir, aggregateCommitLockName)
	if err := c.ops.before("ownership-create"); err != nil {
		return errors.Join(fmt.Errorf("acquire aggregate output ownership: %w", err), c.cleanupCreatedDirectories())
	}
	lock, err := c.ops.openFile(c.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 - fixed owner marker under validated output directory
	if err != nil {
		if os.IsExist(err) {
			return errors.Join(fmt.Errorf("aggregate output directory is already owned or needs recovery: %s exists", c.lockPath), c.cleanupCreatedDirectories())
		}
		return errors.Join(fmt.Errorf("acquire aggregate output ownership: %w", err), c.cleanupCreatedDirectories())
	}
	c.lockHeld = true
	if c.durable {
		if err := c.ops.before("ownership-sync"); err != nil {
			primary := fmt.Errorf("sync aggregate ownership marker: %w", err)
			return errors.Join(c.closeOwnershipAfterFailure(lock, primary), c.abort())
		}
		if err := c.ops.syncData(lock); err != nil {
			primary := fmt.Errorf("sync aggregate ownership marker: %w", err)
			return errors.Join(c.closeOwnershipAfterFailure(lock, primary), c.abort())
		}
	}
	if err := c.ops.before("ownership-close"); err != nil {
		primary := fmt.Errorf("close aggregate ownership marker: %w", err)
		return errors.Join(c.closeOwnershipAfterFailure(lock, primary), c.abort())
	}
	if err := c.ops.closeFile(lock); err != nil {
		return errors.Join(fmt.Errorf("close aggregate ownership marker: %w", err), c.abort())
	}
	if c.durable {
		if err := c.syncDirectory("ownership-dir-sync"); err != nil {
			return errors.Join(err, c.abort())
		}
	}

	entries, err := c.ops.readDir(c.outputDir)
	if err != nil {
		return errors.Join(fmt.Errorf("inspect aggregate output directory: %w", err), c.abort())
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == aggregateCommitLockName {
			continue
		}
		if aggregateCommitArtifactName(name) {
			return errors.Join(fmt.Errorf("aggregate output directory contains prior or interrupted commit artifact %q; use a fresh output location or reconcile it before retrying", name), c.abort())
		}
	}
	c.prepared = true
	return nil
}

func (c *localAggregateCommit) closeOwnershipAfterFailure(f *os.File, primary error) error {
	if err := c.ops.before("ownership-cleanup-close"); err != nil {
		return errors.Join(primary, fmt.Errorf("close aggregate ownership marker during cleanup: %w", err))
	}
	if err := c.ops.closeFile(f); err != nil {
		return errors.Join(primary, fmt.Errorf("close aggregate ownership marker during cleanup: %w", err))
	}
	return primary
}

func aggregateCommitArtifactName(name string) bool {
	if name == "manifest.json" || name == "failures.json" || name == "dispositions.json" || name == "records.jsonl" {
		return true
	}
	if strings.HasPrefix(name, "records-") && strings.HasSuffix(name, ".jsonl") {
		return true
	}
	return strings.HasSuffix(name, ".partial") || strings.HasPrefix(name, ".manifest.json.") || strings.HasPrefix(name, ".failures.json.") || strings.HasPrefix(name, ".dispositions.json.")
}

func existingDirectoryAncestor(path string) (string, error) {
	ancestor := filepath.Clean(path)
	for {
		info, err := os.Stat(ancestor)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("aggregate output path is not a directory: %s", ancestor)
			}
			return ancestor, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect aggregate output filesystem: %w", err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("find existing aggregate output filesystem ancestor for %s", path)
		}
		ancestor = parent
	}
}

// probeExistingDirectorySync verifies the directory-sync operation before any
// aggregate output mutation. The explicit durability opt-out still runs this
// support probe so it cannot bypass the platform/filesystem safety gate.
func probeExistingDirectorySync(path string) error {
	ancestor, err := existingDirectoryAncestor(path)
	if err != nil {
		return err
	}
	dir, err := os.Open(ancestor) // #nosec G304 - validated ancestor of the operator-provided aggregate output path
	if err != nil {
		return fmt.Errorf("open aggregate output filesystem ancestor for sync: %w", err)
	}
	syncErr := platformSyncDirectory(dir)
	closeErr := dir.Close()
	if syncErr != nil {
		syncErr = fmt.Errorf("sync aggregate output filesystem ancestor: %w", syncErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close aggregate output filesystem ancestor: %w", closeErr)
	}
	return errors.Join(syncErr, closeErr)
}

func (c *localAggregateCommit) ensureDirectoryDurable(path string) error {
	missing := make([]string, 0, 4)
	cur := filepath.Clean(path)
	for {
		info, err := c.ops.stat(cur)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("aggregate output path is not a directory: %s", cur)
			}
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat aggregate output directory: %w", err)
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			return fmt.Errorf("find existing aggregate output ancestor for %s", path)
		}
		cur = parent
	}
	// Probe the existing ancestor before creating anything. Unsupported directory
	// sync therefore fails without mutating the requested output ancestry.
	if err := c.syncDirectoryPath(cur, "ancestor-dir-sync"); err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		child := missing[i]
		if err := c.ops.before("directory-create"); err != nil {
			return fmt.Errorf("create aggregate output directory %s: %w", child, err)
		}
		if err := c.ops.mkdir(child, 0o750); err != nil {
			return fmt.Errorf("create aggregate output directory %s: %w", child, err)
		}
		c.createdDirs = append(c.createdDirs, child)
		if err := c.syncDirectoryPath(filepath.Dir(child), "parent-dir-sync"); err != nil {
			return err
		}
	}
	return c.syncDirectoryPath(path, "output-dir-sync")
}

func (c *localAggregateCommit) syncDirectory(step string) error {
	return c.syncDirectoryPath(c.outputDir, step)
}

func (c *localAggregateCommit) syncDirectoryPath(path, step string) error {
	if err := c.ops.before(step + "-open"); err != nil {
		return fmt.Errorf("open directory for durable sync %s: %w", path, err)
	}
	dir, err := c.ops.open(path)
	if err != nil {
		return fmt.Errorf("open directory for durable sync %s: %w", path, err)
	}
	if err := c.ops.before(step); err != nil {
		return c.closeDirectoryAfterFailure(step, dir, fmt.Errorf("sync directory %s: %w", path, err))
	}
	if err := c.ops.syncDir(dir); err != nil {
		return c.closeDirectoryAfterFailure(step, dir, fmt.Errorf("sync directory %s: %w", path, err))
	}
	if err := c.ops.before(step + "-close"); err != nil {
		return c.closeDirectoryAfterFailure(step, dir, fmt.Errorf("close synced directory %s: %w", path, err))
	}
	if err := c.ops.closeFile(dir); err != nil {
		return fmt.Errorf("close synced directory %s: %w", path, err)
	}
	return nil
}

func (c *localAggregateCommit) closeDirectoryAfterFailure(step string, dir *os.File, primary error) error {
	if err := c.ops.before(step + "-cleanup-close"); err != nil {
		return errors.Join(primary, fmt.Errorf("close directory during cleanup: %w", err))
	}
	if err := c.ops.closeFile(dir); err != nil {
		return errors.Join(primary, fmt.Errorf("close directory during cleanup: %w", err))
	}
	return primary
}

func (c *localAggregateCommit) openShard(finalPath string) (*os.File, string, error) {
	if err := c.prepare(); err != nil {
		return nil, "", err
	}
	stagePath := finalPath + ".partial"
	if err := c.ops.before("shard-open"); err != nil {
		return nil, "", fmt.Errorf("open aggregate shard staging: %w", err)
	}
	f, err := c.ops.openFile(stagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 - fixed staging name under exclusively owned output directory
	if err != nil {
		return nil, "", fmt.Errorf("open aggregate shard staging: %w", err)
	}
	c.stagePaths = append(c.stagePaths, stagePath)
	return f, stagePath, nil
}

func (c *localAggregateCommit) beforeShardWrite() error {
	if err := c.ops.before("shard-write"); err != nil {
		return fmt.Errorf("write aggregate record: %w", err)
	}
	return nil
}

func (c *localAggregateCommit) finalizeShard(f *os.File) error {
	if c.durable {
		if err := c.ops.before("shard-sync"); err != nil {
			return fmt.Errorf("sync aggregate shard: %w", err)
		}
		if err := c.ops.syncData(f); err != nil {
			return fmt.Errorf("sync aggregate shard: %w", err)
		}
	}
	if err := c.ops.before("shard-close"); err != nil {
		return fmt.Errorf("close aggregate shard: %w", err)
	}
	if err := c.ops.closeFile(f); err != nil {
		return fmt.Errorf("close aggregate shard: %w", err)
	}
	return nil
}

func (c *localAggregateCommit) commitShards(stagePaths, finalPaths []string) error {
	for i, stage := range stagePaths {
		if err := c.ops.before("shard-rename"); err != nil {
			return fmt.Errorf("commit aggregate shard %s: %w", finalPaths[i], err)
		}
		if err := c.ops.rename(stage, finalPaths[i]); err != nil {
			return fmt.Errorf("commit aggregate shard %s: %w", finalPaths[i], err)
		}
		c.finalPaths = append(c.finalPaths, finalPaths[i])
	}
	if c.durable {
		return c.syncDirectory("shards-dir-sync")
	}
	return nil
}

func (c *localAggregateCommit) writeSidecar(path string, data []byte) error {
	final, err := c.writeAtomicFile("sidecar", path, data)
	if err != nil {
		return err
	}
	c.sidecarPaths = append(c.sidecarPaths, final)
	return nil
}

func (c *localAggregateCommit) writeManifest(path string, data []byte) error {
	final, err := c.writeAtomicFile("manifest", path, data)
	if err != nil {
		return err
	}
	c.markerRenamed = true
	if c.durable {
		if err := c.syncDirectory("manifest-dir-sync"); err != nil {
			invalidateErr := c.invalidateMarker()
			return errors.Join(fmt.Errorf("aggregate commit is unconfirmed after manifest rename: %w", err), invalidateErr)
		}
	}
	c.committed = true
	_ = final
	return nil
}

func (c *localAggregateCommit) writeAtomicFile(kind, path string, data []byte) (string, error) {
	if err := c.prepare(); err != nil {
		return "", err
	}
	if filepath.Clean(filepath.Dir(path)) != c.outputDir {
		return "", fmt.Errorf("%s must be written inside the owned aggregate output directory", filepath.Base(path))
	}
	if _, err := c.ops.stat(path); err == nil {
		return "", fmt.Errorf("refusing to replace existing aggregate %s %s", kind, path)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect aggregate %s destination: %w", kind, err)
	}
	if err := c.ops.before(kind + "-temp-create"); err != nil {
		return "", fmt.Errorf("create aggregate %s temporary file: %w", kind, err)
	}
	f, err := c.ops.createTemp(c.outputDir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create aggregate %s temporary file: %w", kind, err)
	}
	tempPath := f.Name()
	c.tempPaths = append(c.tempPaths, tempPath)
	w := bufio.NewWriter(f)
	if err := c.ops.before(kind + "-write"); err != nil {
		return "", c.closeTemporaryAfterFailure(kind, f, fmt.Errorf("write aggregate %s: %w", kind, err))
	}
	n, err := w.Write(data)
	if err != nil {
		return "", c.closeTemporaryAfterFailure(kind, f, fmt.Errorf("write aggregate %s: %w", kind, err))
	}
	if n != len(data) {
		return "", c.closeTemporaryAfterFailure(kind, f, fmt.Errorf("write aggregate %s: short write %d of %d bytes", kind, n, len(data)))
	}
	if err := c.ops.before(kind + "-flush"); err != nil {
		return "", c.closeTemporaryAfterFailure(kind, f, fmt.Errorf("flush aggregate %s: %w", kind, err))
	}
	if err := w.Flush(); err != nil {
		return "", c.closeTemporaryAfterFailure(kind, f, fmt.Errorf("flush aggregate %s: %w", kind, err))
	}
	if c.durable {
		if err := c.ops.before(kind + "-sync"); err != nil {
			return "", c.closeTemporaryAfterFailure(kind, f, fmt.Errorf("sync aggregate %s: %w", kind, err))
		}
		if err := c.ops.syncData(f); err != nil {
			return "", c.closeTemporaryAfterFailure(kind, f, fmt.Errorf("sync aggregate %s: %w", kind, err))
		}
	}
	if err := c.ops.before(kind + "-close"); err != nil {
		return "", c.closeTemporaryAfterFailure(kind, f, fmt.Errorf("close aggregate %s: %w", kind, err))
	}
	if err := c.ops.closeFile(f); err != nil {
		return "", fmt.Errorf("close aggregate %s: %w", kind, err)
	}
	if kind == "manifest" {
		// Keep exclusive ownership through temp write/sync/close. Release it only
		// at the reader-marker publication boundary; final shards (and the temp
		// until this rename) ensure any competing owner still rejects the directory.
		if err := c.releaseLock(); err != nil {
			return "", err
		}
	}
	if err := c.ops.before(kind + "-rename"); err != nil {
		return "", fmt.Errorf("rename aggregate %s: %w", kind, err)
	}
	if err := c.ops.rename(tempPath, path); err != nil {
		return "", fmt.Errorf("rename aggregate %s: %w", kind, err)
	}
	return path, nil
}

func (c *localAggregateCommit) closeTemporaryAfterFailure(kind string, f *os.File, primary error) error {
	if err := c.ops.before(kind + "-cleanup-close"); err != nil {
		return errors.Join(primary, fmt.Errorf("close aggregate %s during cleanup: %w", kind, err))
	}
	if err := c.ops.closeFile(f); err != nil {
		return errors.Join(primary, fmt.Errorf("close aggregate %s during cleanup: %w", kind, err))
	}
	return primary
}

func (c *localAggregateCommit) releaseLock() error {
	if !c.lockHeld {
		return nil
	}
	if err := c.ops.before("ownership-remove"); err != nil {
		return fmt.Errorf("release aggregate output ownership: %w", err)
	}
	if err := c.ops.remove(c.lockPath); err != nil {
		return fmt.Errorf("release aggregate output ownership: %w", err)
	}
	c.lockHeld = false
	return nil
}

func (c *localAggregateCommit) invalidateMarker() error {
	if !c.markerRenamed {
		return nil
	}
	manifestPath := filepath.Join(c.outputDir, "manifest.json")
	if err := c.ops.before("manifest-invalidate"); err != nil {
		return fmt.Errorf("invalidate unconfirmed aggregate manifest: %w", err)
	}
	if err := c.ops.remove(manifestPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("invalidate unconfirmed aggregate manifest: %w", err)
	}
	c.markerRenamed = false
	if c.durable {
		return c.syncDirectory("manifest-invalidate-dir-sync")
	}
	return nil
}

func (c *localAggregateCommit) abort() error {
	if c == nil || c.committed {
		return nil
	}
	var errs []error
	if err := c.invalidateMarker(); err != nil {
		errs = append(errs, err)
	}
	owned := make([]string, 0, len(c.tempPaths)+len(c.stagePaths)+len(c.finalPaths)+len(c.sidecarPaths))
	owned = append(owned, c.tempPaths...)
	owned = append(owned, c.stagePaths...)
	owned = append(owned, c.finalPaths...)
	owned = append(owned, c.sidecarPaths...)
	for _, path := range owned {
		if err := c.ops.before("cleanup-remove"); err != nil {
			errs = append(errs, fmt.Errorf("clean aggregate commit path %s: %w", path, err))
			continue
		}
		if err := c.ops.remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("clean aggregate commit path %s: %w", path, err))
		}
	}
	if c.lockHeld {
		if err := c.releaseLock(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.durable && c.lockPath != "" {
		if err := c.syncDirectory("cleanup-dir-sync"); err != nil {
			errs = append(errs, err)
		}
	}
	if err := c.cleanupCreatedDirectories(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (c *localAggregateCommit) cleanupCreatedDirectories() error {
	if c == nil || len(c.createdDirs) == 0 {
		return nil
	}
	var errs []error
	for i := len(c.createdDirs) - 1; i >= 0; i-- {
		dir := c.createdDirs[i]
		if err := c.ops.before("cleanup-directory-remove"); err != nil {
			errs = append(errs, fmt.Errorf("clean aggregate output directory %s: %w", dir, err))
			continue
		}
		if err := c.ops.remove(dir); err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("clean aggregate output directory %s: %w", dir, err))
			}
			continue
		}
		if c.durable {
			if err := c.syncDirectoryPath(filepath.Dir(dir), "cleanup-parent-dir-sync"); err != nil {
				errs = append(errs, err)
			}
		}
	}
	c.createdDirs = nil
	return errors.Join(errs...)
}
