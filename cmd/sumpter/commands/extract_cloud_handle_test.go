package commands

import (
	"testing"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

// TestResolvedCredentialHandles covers input/output cloud handle resolution: an
// explicit selector wins, otherwise the default handle is used. The recipe tier
// is folded onto these option fields by the recipe runner before runExtract, so
// this also exercises the recipe>default fallback (recipe value present on the
// field) vs CLI-override (same field, set by the CLI).
func TestResolvedCredentialHandles(t *testing.T) {
	cases := []struct {
		name    string
		in, out string
		wantIn  string
		wantOut string
	}{
		{"both default", "", "", uriio.DefaultHandleName, uriio.DefaultHandleName},
		{"explicit input", "reader", "", "reader", uriio.DefaultHandleName},
		{"explicit output", "", "writer", uriio.DefaultHandleName, "writer"},
		{"independent in/out", "reader", "writer", "reader", "writer"},
		{"whitespace falls back", "  ", "  ", uriio.DefaultHandleName, uriio.DefaultHandleName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := &ExtractOptions{InputCredentialsHandle: tc.in, OutputCredentialsHandle: tc.out}
			if got := resolvedInputHandle(opts); got != tc.wantIn {
				t.Errorf("resolvedInputHandle = %q, want %q", got, tc.wantIn)
			}
			if got := resolvedOutputHandle(opts); got != tc.wantOut {
				t.Errorf("resolvedOutputHandle = %q, want %q", got, tc.wantOut)
			}
		})
	}
}
