//go:build !darwin && !linux

package commands

import (
	"fmt"
	"os"
	"runtime"
)

func validateLocalDurableCommitSupport(string) error {
	return fmt.Errorf("local aggregate output is unsupported on %s: durable directory commit semantics are verified only on Darwin and Linux", runtime.GOOS)
}

func platformSyncData(*os.File) error      { return validateLocalDurableCommitSupport("") }
func platformSyncDirectory(*os.File) error { return validateLocalDurableCommitSupport("") }
