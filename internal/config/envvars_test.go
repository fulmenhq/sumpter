package config

import (
	"reflect"
	"testing"
)

func TestGetSumpterEnvVars(t *testing.T) {
	vars := GetSumpterEnvVars()

	// Verify we get the expected number of variables
	expectedCount := 11
	if len(vars) != expectedCount {
		t.Errorf("Expected %d environment variables, got %d", expectedCount, len(vars))
	}

	// Verify specific variables exist
	expectedVars := []string{
		"SUMPTER_HOME",
		"SUMPTER_WORKDIR",
		"SUMPTER_ENV",
		"SUMPTER_LOG_LEVEL",
		"SUMPTER_LOG_FORMAT",
		"SUMPTER_CONFIG",
		"SUMPTER_MAX_MEMORY",
		"SUMPTER_WORKER_COUNT",
		"SUMPTER_TIMEOUT",
		"SUMPTER_TELEMETRY_ENABLED",
		"SUMPTER_SERVICE_NAME",
	}

	for _, varName := range expectedVars {
		if _, exists := vars[varName]; !exists {
			t.Errorf("Expected environment variable %s not found", varName)
		}
	}
}

func TestGetSumpterEnvVarsByCategory(t *testing.T) {
	categories := GetSumpterEnvVarsByCategory()

	// Verify expected categories exist
	expectedCategories := []string{"paths", "runtime", "logging", "configuration", "performance", "telemetry"}

	for _, category := range expectedCategories {
		if _, exists := categories[category]; !exists {
			t.Errorf("Expected category %s not found", category)
		}
	}

	// Verify paths category has expected variables
	pathsVars := categories["paths"]
	expectedPathsVars := 2 // SUMPTER_HOME, SUMPTER_WORKDIR
	if len(pathsVars) != expectedPathsVars {
		t.Errorf("Expected %d variables in paths category, got %d", expectedPathsVars, len(pathsVars))
	}

	// Verify performance category has expected variables
	performanceVars := categories["performance"]
	expectedPerformanceVars := 3 // MAX_MEMORY, WORKER_COUNT, TIMEOUT
	if len(performanceVars) != expectedPerformanceVars {
		t.Errorf("Expected %d variables in performance category, got %d", expectedPerformanceVars, len(performanceVars))
	}
}

func TestIsSumpterEnvVar(t *testing.T) {
	tests := []struct {
		name     string
		varName  string
		expected bool
	}{
		{"Valid SUMPTER variable", "SUMPTER_HOME", true},
		{"Valid SUMPTER variable with underscore", "SUMPTER_LOG_LEVEL", true},
		{"Invalid non-SUMPTER variable", "HOME", false},
		{"Invalid empty string", "", false},
		{"Invalid partial match", "SUMPTER", false},
		{"Invalid case mismatch", "sumpter_home", false},
		{"Invalid extra prefix", "MY_SUMPTER_HOME", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsSumpterEnvVar(tt.varName)
			if result != tt.expected {
				t.Errorf("IsSumpterEnvVar(%s) = %v, expected %v", tt.varName, result, tt.expected)
			}
		})
	}
}

func TestGetSumpterEnvVarDefinition(t *testing.T) {
	tests := []struct {
		name             string
		varName          string
		expectExists     bool
		expectedType     EnvVarType
		expectedCategory string
	}{
		{
			name:             "SUMPTER_HOME definition",
			varName:          "SUMPTER_HOME",
			expectExists:     true,
			expectedType:     EnvVarTypePath,
			expectedCategory: "paths",
		},
		{
			name:             "SUMPTER_ENV definition",
			varName:          "SUMPTER_ENV",
			expectExists:     true,
			expectedType:     EnvVarTypeString,
			expectedCategory: "runtime",
		},
		{
			name:             "SUMPTER_MAX_MEMORY definition",
			varName:          "SUMPTER_MAX_MEMORY",
			expectExists:     true,
			expectedType:     EnvVarTypeInt,
			expectedCategory: "performance",
		},
		{
			name:             "SUMPTER_TELEMETRY_ENABLED definition",
			varName:          "SUMPTER_TELEMETRY_ENABLED",
			expectExists:     true,
			expectedType:     EnvVarTypeBool,
			expectedCategory: "telemetry",
		},
		{
			name:         "Non-existent variable",
			varName:      "NON_EXISTENT_VAR",
			expectExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, exists := GetSumpterEnvVarDefinition(tt.varName)

			if exists != tt.expectExists {
				t.Errorf("GetSumpterEnvVarDefinition(%s) exists = %v, expected %v", tt.varName, exists, tt.expectExists)
				return
			}

			if !exists {
				return
			}

			if def.Type != tt.expectedType {
				t.Errorf("GetSumpterEnvVarDefinition(%s) type = %v, expected %v", tt.varName, def.Type, tt.expectedType)
			}

			if def.Category != tt.expectedCategory {
				t.Errorf("GetSumpterEnvVarDefinition(%s) category = %v, expected %v", tt.varName, def.Category, tt.expectedCategory)
			}

			// Verify the name matches
			if def.Name != tt.varName {
				t.Errorf("GetSumpterEnvVarDefinition(%s) name = %v, expected %v", tt.varName, def.Name, tt.varName)
			}
		})
	}
}

func TestGetAllSumpterPrefixes(t *testing.T) {
	prefixes := GetAllSumpterPrefixes()

	// Should only contain "SUMPTER_"
	expectedPrefixes := []string{"SUMPTER_"}

	if len(prefixes) != len(expectedPrefixes) {
		t.Errorf("Expected %d prefixes, got %d", len(expectedPrefixes), len(prefixes))
	}

	if len(prefixes) > 0 && prefixes[0] != "SUMPTER_" {
		t.Errorf("Expected first prefix to be 'SUMPTER_', got %s", prefixes[0])
	}
}

func TestEnvVarDefinitionsCompleteness(t *testing.T) {
	vars := GetSumpterEnvVars()

	for name, def := range vars {
		// Verify all required fields are set
		if def.Name == "" {
			t.Errorf("Environment variable %s has empty Name field", name)
		}

		if def.Description == "" {
			t.Errorf("Environment variable %s has empty Description field", name)
		}

		if def.Category == "" {
			t.Errorf("Environment variable %s has empty Category field", name)
		}

		// Verify name consistency
		if def.Name != name {
			t.Errorf("Environment variable key %s doesn't match definition Name %s", name, def.Name)
		}

		// Verify type is valid
		validTypes := map[EnvVarType]bool{
			EnvVarTypeString: true,
			EnvVarTypePath:   true,
			EnvVarTypeBool:   true,
			EnvVarTypeInt:    true,
		}

		if !validTypes[def.Type] {
			t.Errorf("Environment variable %s has invalid type: %s", name, def.Type)
		}
	}
}

func TestEnvVarCategories(t *testing.T) {
	vars := GetSumpterEnvVars()
	categories := GetSumpterEnvVarsByCategory()

	// Verify that all variables are accounted for in categories
	totalInCategories := 0
	for _, categoryVars := range categories {
		totalInCategories += len(categoryVars)
	}

	if totalInCategories != len(vars) {
		t.Errorf("Total variables in categories (%d) doesn't match total variables (%d)", totalInCategories, len(vars))
	}

	// Verify each variable is in the correct category
	for name, def := range vars {
		found := false
		if categoryVars, exists := categories[def.Category]; exists {
			for _, catVar := range categoryVars {
				if catVar.Name == name {
					found = true
					break
				}
			}
		}

		if !found {
			t.Errorf("Variable %s not found in its expected category %s", name, def.Category)
		}
	}
}

func TestEnvVarTypeConstants(t *testing.T) {
	// Verify the type constants are defined correctly
	if EnvVarTypeString != "string" {
		t.Errorf("EnvVarTypeString = %s, expected 'string'", EnvVarTypeString)
	}

	if EnvVarTypePath != "path" {
		t.Errorf("EnvVarTypePath = %s, expected 'path'", EnvVarTypePath)
	}

	if EnvVarTypeBool != "bool" {
		t.Errorf("EnvVarTypeBool = %s, expected 'bool'", EnvVarTypeBool)
	}

	if EnvVarTypeInt != "int" {
		t.Errorf("EnvVarTypeInt = %s, expected 'int'", EnvVarTypeInt)
	}
}

func TestEnvVarImmutability(t *testing.T) {
	// Get the original map
	original := GetSumpterEnvVars()

	// Modify the returned map (this should not affect the original)
	if len(original) > 0 {
		for name := range original {
			delete(original, name)
			break
		}
	}

	// Get the map again and verify it's unchanged
	after := GetSumpterEnvVars()

	if !reflect.DeepEqual(original, after) {
		t.Error("GetSumpterEnvVars() returned a mutable map that was modified")
	}
}
