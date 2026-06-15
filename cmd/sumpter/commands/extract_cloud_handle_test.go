package commands

import (
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

// TestValidateCredentialOptionsHandleSelectors proves the --input/output-
// credentials-handle selectors are validated with the shared handle-name rule up
// front: a malformed selector fails with an actionable invalid-handle-name error
// naming the flag, while empty selectors (default handle) and valid slugs pass.
func TestValidateCredentialOptionsHandleSelectors(t *testing.T) {
	if err := validateCredentialOptions(&ExtractOptions{InputCredentialsHandle: "reader", OutputCredentialsHandle: "writer"}); err != nil {
		t.Errorf("valid selectors = %v, want nil", err)
	}
	if err := validateCredentialOptions(&ExtractOptions{}); err != nil {
		t.Errorf("empty selectors = %v, want nil (default handle)", err)
	}

	err := validateCredentialOptions(&ExtractOptions{InputCredentialsHandle: "bad handle"})
	if err == nil || !strings.Contains(err.Error(), "--input-credentials-handle") || !strings.Contains(err.Error(), "invalid credentials handle name") {
		t.Errorf("malformed input selector = %v, want a flag-named invalid-handle-name error", err)
	}
	err = validateCredentialOptions(&ExtractOptions{OutputCredentialsHandle: "bad/handle"})
	if err == nil || !strings.Contains(err.Error(), "--output-credentials-handle") || !strings.Contains(err.Error(), "invalid credentials handle name") {
		t.Errorf("malformed output selector = %v, want a flag-named invalid-handle-name error", err)
	}
}

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
