package assets

import (
	"testing"
)

func TestGetDocsFS(t *testing.T) {
	fs, err := GetDocsFS()
	if err != nil {
		t.Errorf("GetDocsFS() error = %v", err)
		return
	}
	if fs == nil {
		t.Error("GetDocsFS() returned nil filesystem")
	}
}

func TestGetSchemasFS(t *testing.T) {
	fs, err := GetSchemasFS()
	if err != nil {
		t.Errorf("GetSchemasFS() error = %v", err)
		return
	}
	if fs == nil {
		t.Error("GetSchemasFS() returned nil filesystem")
	}
}

func TestGetExamplesFS(t *testing.T) {
	fs, err := GetExamplesFS()
	if err != nil {
		t.Errorf("GetExamplesFS() error = %v", err)
		return
	}
	if fs == nil {
		t.Error("GetExamplesFS() returned nil filesystem")
	}
}
