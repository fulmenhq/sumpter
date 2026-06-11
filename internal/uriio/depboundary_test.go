package uriio_test

import (
	"os/exec"
	"strings"
	"testing"
)

// allowedGonimbusPackages is the gonimbus library-consumer surface Sumpter is
// permitted to import (docs/library-consumers.md). Importing anything outside
// this set — CLI command packages, the server, or the index-store substrate —
// is a dependency-boundary violation.
var allowedGonimbusPackages = map[string]bool{
	"github.com/3leaps/gonimbus/pkg/uri":           true,
	"github.com/3leaps/gonimbus/pkg/match":         true,
	"github.com/3leaps/gonimbus/pkg/provider":      true,
	"github.com/3leaps/gonimbus/pkg/provider/s3":   true,
	"github.com/3leaps/gonimbus/pkg/provider/file": true,
	"github.com/3leaps/gonimbus/pkg/content":       true,
}

// deniedDependencies are heavyweight CLI/server/index-store dependencies that
// must never enter the Sumpter build graph through the gonimbus seam. Matched as
// substrings of the full import path so versioned/nested forms are caught.
var deniedDependencies = []string{
	"github.com/spf13/viper",
	"github.com/go-chi/chi",
	"go-libsql",
	"libsql",
	"mattn/go-sqlite3",
	"modernc.org/sqlite",
	"3leaps/gofulmen",
}

// seamDeps returns the deduplicated transitive dependency import paths reachable
// from the uriio seam via `go list -deps`.
//
// The boundary is scoped to the seam package, not the whole module: gonimbus is
// the only dependency that enters Sumpter through uriio, so this closure is
// exactly the surface the boundary protects. (The wider module legitimately uses
// some of the "denied" libraries — e.g. viper via the cobra CLI — which is
// irrelevant to the gonimbus seam and must not be flagged here.)
func seamDeps(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "github.com/fulmenhq/sumpter/internal/uriio")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps failed: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// TestGonimbusImportSurface asserts that every gonimbus package reachable from
// the uriio seam is on the supported library-consumer surface.
func TestGonimbusImportSurface(t *testing.T) {
	for _, dep := range seamDeps(t) {
		if !strings.HasPrefix(dep, "github.com/3leaps/gonimbus/") {
			continue
		}
		if !allowedGonimbusPackages[dep] {
			t.Errorf("disallowed gonimbus import in build graph: %s\n"+
				"only the library-consumer surface (pkg/uri, pkg/match, pkg/provider[/s3|/file], pkg/content) is permitted",
				dep)
		}
	}
}

// TestNoDeniedDependencies asserts that no CLI/server/index-store dependency
// entered the Sumpter build graph through the gonimbus seam.
func TestNoDeniedDependencies(t *testing.T) {
	for _, dep := range seamDeps(t) {
		for _, denied := range deniedDependencies {
			if strings.Contains(dep, denied) {
				t.Errorf("denied dependency in build graph: %s (matched %q)", dep, denied)
			}
		}
	}
}
