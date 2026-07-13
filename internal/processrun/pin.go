package processrun

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
)

// Embedded process-run/v0 pin (contract.json + process-card entry schema).
// Sourced from the same Crucible v0.1.19 baseline fixtures used by C0 CI.
//
//go:embed embedded/process-run-v0/contract.json
var embeddedContractJSON []byte

//go:embed embedded/process-run-v0/process-card.schema.json
var embeddedCardSchema []byte

var (
	pinOnce     sync.Once
	pinErr      error
	pinResolved *artifactcontract.ResolvedContract
)

// pinnedProcessRunBase materializes the embedded process-run pin to a
// process-private directory and resolves it against ProcessRunBaselineBundleSHA256.
// Failures are sticky (fail-open at the card publish site).
func pinnedProcessRunBase() (*artifactcontract.ResolvedContract, error) {
	pinOnce.Do(func() {
		// Content-addressed dir under TempDir so restarts are cheap and concurrent
		// processes do not stomp each other's materialization.
		sum := sha256.Sum256(append(append([]byte{}, embeddedContractJSON...), embeddedCardSchema...))
		dir := filepath.Join(os.TempDir(), "sumpter-process-run-pin-"+hex.EncodeToString(sum[:8]))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			pinErr = fmt.Errorf("materialize process-run pin: %w", err)
			return
		}
		if err := writeIfMissing(filepath.Join(dir, "contract.json"), embeddedContractJSON); err != nil {
			pinErr = err
			return
		}
		if err := writeIfMissing(filepath.Join(dir, "process-card.schema.json"), embeddedCardSchema); err != nil {
			pinErr = err
			return
		}
		resolved, err := artifactcontract.ResolveProcessRunBaseline(dir)
		if err != nil {
			pinErr = err
			return
		}
		_ = dir
		pinResolved = resolved
	})
	if pinErr != nil {
		return nil, pinErr
	}
	if pinResolved == nil {
		return nil, ErrCardSchema
	}
	return pinResolved, nil
}

func writeIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		// Another process may have won the rename race.
		if _, serr := os.Stat(path); serr == nil {
			return nil
		}
		return err
	}
	return nil
}

// resolveCardValidator returns the process-run baseline used to validate cards.
// ContractBase, when non-empty, is an operator/test override of the embedded pin
// and must still match ProcessRunBaselineBundleSHA256.
func resolveCardValidator(contractBase string) (*artifactcontract.ResolvedContract, error) {
	if contractBase != "" {
		return artifactcontract.ResolveProcessRunBaseline(contractBase)
	}
	return pinnedProcessRunBase()
}
