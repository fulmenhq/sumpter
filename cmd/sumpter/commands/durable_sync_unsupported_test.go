//go:build !darwin && !linux

package commands

import (
	"runtime"
	"strings"
	"testing"

	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
)

func TestLocalAggregateRejectedOnUnsupportedPlatformWithAndWithoutOptOut(t *testing.T) {
	for _, optOut := range []bool{false, true} {
		opts := &ExtractOptions{
			OutputPath:      t.TempDir(),
			OutputMode:      outputModeAggregate,
			NoDurableCommit: optOut,
		}
		err := validateAggregateOptions(opts, []string{recipesmanifest.OutputFormatNDJSON})
		if err == nil || !strings.Contains(err.Error(), "unsupported on "+runtime.GOOS) {
			t.Fatalf("optOut=%v error=%v, want unsupported-platform rejection", optOut, err)
		}
	}
}
