package uriio

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	gonimbusprovider "github.com/3leaps/gonimbus/pkg/provider"
)

func TestClassifyHeadError(t *testing.T) {
	const uri = "s3://bucket/key.json"
	if err := classifyHeadError(nil, uri, nil); err != nil {
		t.Fatalf("nil error classified as %v", err)
	}
	notFound := classifyHeadError(fmt.Errorf("head: %w", gonimbusprovider.ErrNotFound), uri, nil)
	if !errors.Is(notFound, ErrObjectNotFound) || errors.Is(notFound, gonimbusprovider.ErrNotFound) {
		t.Fatalf("not found = %v; want ErrObjectNotFound without wrapping the provider error", notFound)
	}
	denied := classifyHeadError(fmt.Errorf("head: %w", gonimbusprovider.ErrAccessDenied), uri, nil)
	if !errors.Is(denied, ErrObjectAccessDenied) {
		t.Fatalf("denied = %v; want ErrObjectAccessDenied", denied)
	}
	other := classifyHeadError(errors.New("secret-token timeout"), uri, []string{"secret-token"})
	if errors.Is(other, ErrObjectNotFound) || errors.Is(other, ErrObjectAccessDenied) {
		t.Fatalf("other error misclassified: %v", other)
	}
	if got := other.Error(); got == "" || strings.Contains(got, "secret-token") {
		t.Fatalf("other error not redacted: %q", got)
	}
}
