package config

import (
	"testing"
)

func TestValidRealms(t *testing.T) {
	realms := ValidRealms()
	if len(realms) == 0 {
		t.Error("ValidRealms() returned empty slice")
	}

	expectedRealms := []string{"retail", "finance", "healthcare", "environment", "legal", "general"}
	if len(realms) != len(expectedRealms) {
		t.Errorf("ValidRealms() returned %d realms, expected %d", len(realms), len(expectedRealms))
	}

	for i, expected := range expectedRealms {
		if i >= len(realms) || realms[i] != expected {
			t.Errorf("ValidRealms()[%d] = %v, expected %v", i, realms[i], expected)
		}
	}
}

func TestIsValidRealm(t *testing.T) {
	tests := []struct {
		realm    string
		expected bool
	}{
		{"retail", true},
		{"finance", true},
		{"healthcare", true},
		{"environment", true},
		{"legal", true},
		{"general", true},
		{"invalid", false},
		{"", false},
		{"Retail", false}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.realm, func(t *testing.T) {
			result := IsValidRealm(tt.realm)
			if result != tt.expected {
				t.Errorf("IsValidRealm(%q) = %v, expected %v", tt.realm, result, tt.expected)
			}
		})
	}
}
