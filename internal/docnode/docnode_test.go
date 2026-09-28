package docnode

import (
	"errors"
	"io"
	"testing"

	"github.com/antchfx/xpath"
)

type fakeFormat struct{ token string }

func (f fakeFormat) Token() string                   { return f.token }
func (fakeFormat) Parse(io.Reader) (Document, error) { return nil, ErrRouteUnsupported }
func (fakeFormat) ParseRecord(*Record) (Document, error) {
	return nil, ErrRouteUnsupported
}
func (fakeFormat) NewScanner(io.Reader, string, bool) (RecordScanner, error) {
	return nil, ErrRouteUnsupported
}
func (fakeFormat) NodeOf(xpath.NodeNavigator) (Node, bool) { return nil, false }

func TestRegisterAndLookup(t *testing.T) {
	Register(fakeFormat{token: "docnode-test"})
	f, ok := Lookup("docnode-test")
	if !ok || f.Token() != "docnode-test" {
		t.Fatalf("Lookup(docnode-test) = %v, %v", f, ok)
	}
	if _, ok := Lookup("no-such-format"); ok {
		t.Fatal("Lookup of an unknown token succeeded")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	Register(fakeFormat{token: "docnode-dup"})
	defer func() {
		if recover() == nil {
			t.Fatal("registering a duplicate token did not panic")
		}
	}()
	Register(fakeFormat{token: "docnode-dup"})
}

func TestDefaultIsXML(t *testing.T) {
	if Default != "xml" {
		t.Fatalf("Default = %q, want xml", Default)
	}
}

func TestContextErrorKeepsText(t *testing.T) {
	inner := errors.New("record fragment has no root element")
	var err error = &ContextError{Err: inner}
	if err.Error() != inner.Error() {
		t.Fatalf("ContextError text %q, want %q", err.Error(), inner.Error())
	}
	if !errors.Is(err, inner) {
		t.Fatal("ContextError does not unwrap to its cause")
	}
	var ce *ContextError
	if !errors.As(err, &ce) {
		t.Fatal("errors.As did not find *ContextError")
	}
}
