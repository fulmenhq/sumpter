package provenance

import (
	"regexp"
	"strings"
	"testing"
)

func TestCanonicalizeYAML_MapOrderEquivalent(t *testing.T) {
	left, err := CanonicalizeYAML([]byte("b: 2\na: 1\n"))
	if err != nil {
		t.Fatalf("CanonicalizeYAML left: %v", err)
	}
	right, err := CanonicalizeYAML([]byte("a: 1\nb: 2\n"))
	if err != nil {
		t.Fatalf("CanonicalizeYAML right: %v", err)
	}

	if string(left) != `{"a":1,"b":2}` {
		t.Fatalf("canonical JSON = %s", left)
	}
	if string(left) != string(right) {
		t.Fatalf("map order changed canonical bytes:\nleft:  %s\nright: %s", left, right)
	}
}

func TestRecipeContentHash_FormatAndStability(t *testing.T) {
	signatureA := []byte("signature_id: sig\nname: Test\n")
	signatureB := []byte("name: Test\nsignature_id: sig\n")
	extractA := []byte("record_type: sale\nfield_mappings:\n  - output_field: total\n    xpath: Total\n    type: number\n")
	extractB := []byte("field_mappings:\n  - type: number\n    xpath: Total\n    output_field: total\nrecord_type: sale\n")

	hashA, err := RecipeContentHash(signatureA, extractA)
	if err != nil {
		t.Fatalf("RecipeContentHash A: %v", err)
	}
	hashB, err := RecipeContentHash(signatureB, extractB)
	if err != nil {
		t.Fatalf("RecipeContentHash B: %v", err)
	}

	if hashA != hashB {
		t.Fatalf("equivalent YAML produced different hashes:\nA: %s\nB: %s", hashA, hashB)
	}

	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(hashA) {
		t.Fatalf("hash format = %q", hashA)
	}
}

func TestCanonicalizeYAML_NumberBehavior(t *testing.T) {
	intJSON, err := CanonicalizeYAML([]byte("value: 1\n"))
	if err != nil {
		t.Fatalf("CanonicalizeYAML int: %v", err)
	}
	floatJSON, err := CanonicalizeYAML([]byte("value: 1.0\n"))
	if err != nil {
		t.Fatalf("CanonicalizeYAML float: %v", err)
	}
	if string(intJSON) != string(floatJSON) {
		t.Fatalf("1 and 1.0 should canonicalize identically:\nint:   %s\nfloat: %s", intJSON, floatJSON)
	}

	if _, err := CanonicalizeYAML([]byte("value: 9007199254740992\n")); err == nil {
		t.Fatal("expected unsafe big integer to fail")
	}
	if _, err := CanonicalizeYAML([]byte("value: .nan\n")); err == nil {
		t.Fatal("expected NaN to fail")
	}
	if _, err := CanonicalizeYAML([]byte("value: .inf\n")); err == nil {
		t.Fatal("expected Inf to fail")
	}
}

func TestCanonicalizeYAML_NilVsEmpty(t *testing.T) {
	nilJSON, err := CanonicalizeYAML([]byte("value:\n"))
	if err != nil {
		t.Fatalf("CanonicalizeYAML nil: %v", err)
	}
	emptyMapJSON, err := CanonicalizeYAML([]byte("value: {}\n"))
	if err != nil {
		t.Fatalf("CanonicalizeYAML empty map: %v", err)
	}
	emptySliceJSON, err := CanonicalizeYAML([]byte("value: []\n"))
	if err != nil {
		t.Fatalf("CanonicalizeYAML empty slice: %v", err)
	}

	if string(nilJSON) == string(emptyMapJSON) || string(nilJSON) == string(emptySliceJSON) || string(emptyMapJSON) == string(emptySliceJSON) {
		t.Fatalf("nil, empty map, and empty slice must remain distinct: %s / %s / %s", nilJSON, emptyMapJSON, emptySliceJSON)
	}
}

func TestCanonicalizeYAML_NormalizesUnicodeToNFC(t *testing.T) {
	composed, err := CanonicalizeYAML([]byte("name: \"é\"\n"))
	if err != nil {
		t.Fatalf("CanonicalizeYAML composed: %v", err)
	}
	decomposed, err := CanonicalizeYAML([]byte("name: \"e\u0301\"\n"))
	if err != nil {
		t.Fatalf("CanonicalizeYAML decomposed: %v", err)
	}

	if string(composed) != string(decomposed) {
		t.Fatalf("NFC-equivalent strings must canonicalize identically:\ncomposed:   %s\ndecomposed: %s", composed, decomposed)
	}
	if !strings.Contains(string(composed), "é") {
		t.Fatalf("canonical JSON should contain NFC string, got %s", composed)
	}
}

func TestCanonicalizeYAML_RejectsDuplicateAfterNFC(t *testing.T) {
	_, err := CanonicalizeYAML([]byte("\"é\": 1\n\"e\u0301\": 2\n"))
	if err == nil {
		t.Fatal("expected duplicate NFC-normalized keys to fail")
	}
}
