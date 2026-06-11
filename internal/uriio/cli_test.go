package uriio_test

import (
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

// TestParseCredentialOverrides asserts the CLI accepts handle=profile references
// only and rejects anything shaped like a raw AWS key (no secrets in argv).
func TestParseCredentialOverrides(t *testing.T) {
	t.Run("valid references", func(t *testing.T) {
		got, err := uriio.ParseCredentialOverrides([]string{"src=prod-readonly", " dst = dest-writer "})
		if err != nil {
			t.Fatalf("ParseCredentialOverrides: %v", err)
		}
		if got["src"] != "prod-readonly" || got["dst"] != "dest-writer" {
			t.Fatalf("parsed map = %v, want src/dst profiles", got)
		}
	})

	rejects := map[string][]string{
		"missing equals":      {"srcprofile"},
		"empty handle":        {"=profile"},
		"empty profile":       {"src="},
		"invalid handle name": {"bad name=profile"},
		"duplicate handle":    {"src=a", "src=b"},
		"raw access key id":   {"src=AKIAIOSFODNN7EXAMPLE"},
		"raw secret key":      {"src=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
	}
	for name, in := range rejects {
		t.Run(name, func(t *testing.T) {
			if _, err := uriio.ParseCredentialOverrides(in); err == nil {
				t.Errorf("ParseCredentialOverrides(%v) = nil error, want rejection", in)
			}
		})
	}

	t.Run("raw-key rejection is actionable", func(t *testing.T) {
		_, err := uriio.ParseCredentialOverrides([]string{"src=AKIAIOSFODNN7EXAMPLE"})
		if err == nil || !strings.Contains(err.Error(), "profile name") {
			t.Fatalf("error = %v, want guidance to use a profile name", err)
		}
	})
}
