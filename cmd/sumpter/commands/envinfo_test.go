package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	goneatschema "github.com/fulmenhq/goneat/pkg/schema"
	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/spf13/cobra"
)

func TestEnvInfoSubcommands(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "sumpter-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Set up test environment variables
	testEnvVars := map[string]string{
		"SUMPTER_HOME":      tempDir,
		"SUMPTER_WORKDIR":   filepath.Join(tempDir, "work"),
		"SUMPTER_LOG_LEVEL": "debug",
		"HOME":              "/tmp/test-home",
		"USER":              "testuser",
		"PATH":              "/usr/bin:/bin",
	}

	for key, value := range testEnvVars {
		_ = os.Setenv(key, value)
		defer func(k string) { _ = os.Unsetenv(k) }(key)
	}

	tests := []struct {
		name         string
		subcommand   string
		args         []string
		expectError  bool
		validateJSON func(t *testing.T, output string)
	}{
		{
			name:       "system subcommand",
			subcommand: "system",
			args:       []string{},
			validateJSON: func(t *testing.T, output string) {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(output), &data); err != nil {
					t.Errorf("Invalid JSON output: %v", err)
					return
				}

				required := []string{"os", "architecture", "goVersion", "numCPU", "hostname", "workingDir", "timestamp"}
				for _, field := range required {
					if _, exists := data[field]; !exists {
						t.Errorf("Missing required field: %s", field)
					}
				}
			},
		},
		{
			name:       "paths subcommand",
			subcommand: "paths",
			args:       []string{},
			validateJSON: func(t *testing.T, output string) {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(output), &data); err != nil {
					t.Errorf("Invalid JSON output: %v", err)
					return
				}

				required := []string{"home", "workDir", "cache", "logs", "configs", "temp"}
				for _, field := range required {
					if _, exists := data[field]; !exists {
						t.Errorf("Missing required field: %s", field)
					}
				}
			},
		},
		{
			name:       "vars subcommand",
			subcommand: "vars",
			args:       []string{},
			validateJSON: func(t *testing.T, output string) {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(output), &data); err != nil {
					t.Errorf("Invalid JSON output: %v", err)
					return
				}

				// Should contain some environment variables
				if len(data) == 0 {
					t.Error("Expected some environment variables in output")
				}

				// Check that sensitive values are redacted
				if pwd, exists := data["PWD"]; exists && pwd != "***redacted***" {
					t.Error("PWD should be redacted for security")
				}
			},
		},
		{
			name:       "xml subcommand",
			subcommand: "xml",
			args:       []string{},
			validateJSON: func(t *testing.T, output string) {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(output), &data); err != nil {
					t.Errorf("Invalid JSON output: %v", err)
					return
				}

				required := []string{"streamingSupported", "encodings", "maxMemoryTarget", "supportedOutputs"}
				for _, field := range required {
					if _, exists := data[field]; !exists {
						t.Errorf("Missing required field: %s", field)
					}
				}

				if streaming, ok := data["streamingSupported"].(bool); !ok || !streaming {
					t.Error("streamingSupported should be true")
				}
			},
		},
		{
			name:       "network subcommand",
			subcommand: "network",
			args:       []string{},
			validateJSON: func(t *testing.T, output string) {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(output), &data); err != nil {
					t.Errorf("Invalid JSON output: %v", err)
					return
				}

				if _, exists := data["interfaces"]; !exists {
					t.Error("Missing interfaces field")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a buffer to capture output
			var buf bytes.Buffer

			// Create the appropriate subcommand
			var cmd *cobra.Command
			switch tt.subcommand {
			case "system":
				cmd = newEnvInfoSystemCommand()
			case "paths":
				cmd = newEnvInfoPathsCommand()
			case "vars":
				cmd = newEnvInfoVarsCommand()
			case "xml":
				cmd = newEnvInfoXMLCommand()
			case "network":
				cmd = newEnvInfoNetworkCommand()
			default:
				t.Fatalf("Unknown subcommand: %s", tt.subcommand)
			}

			// Set up command
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(append(tt.args, "--json"))

			// Execute command
			err := cmd.Execute()
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
				return
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Validate JSON output
			output := buf.String()
			if strings.TrimSpace(output) == "" {
				t.Error("Expected JSON output but got empty string")
				return
			}

			tt.validateJSON(t, output)
		})
	}
}

func TestEnvInfoMainCommand(t *testing.T) {
	// Test that the main envinfo command still works
	var buf bytes.Buffer

	cmd := NewEnvInfoCommand()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--json"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("Main envinfo command failed: %v", err)
		return
	}

	output := buf.String()
	if strings.TrimSpace(output) == "" {
		t.Error("Expected JSON output from main command")
		return
	}

	// Should contain all sections
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		t.Errorf("Invalid JSON from main command: %v", err)
		return
	}

	required := []string{"system", "variables", "stats", "application"}
	for _, field := range required {
		if _, exists := data[field]; !exists {
			t.Errorf("Main command missing required field: %s", field)
		}
	}
}

func TestEnvVarRegistry(t *testing.T) {
	// Test the environment variable registry
	vars := config.GetSumpterEnvVars()

	if len(vars) == 0 {
		t.Error("Expected some SUMPTER environment variables to be registered")
	}

	// Check that SUMPTER_HOME is registered
	if _, exists := vars["SUMPTER_HOME"]; !exists {
		t.Error("SUMPTER_HOME should be registered")
	}

	// Test category grouping
	categories := config.GetSumpterEnvVarsByCategory()
	if len(categories) == 0 {
		t.Error("Expected some categories")
	}

	// Test prefix detection
	prefixes := config.GetAllSumpterPrefixes()
	if len(prefixes) == 0 {
		t.Error("Expected some SUMPTER prefixes")
	}

	found := false
	for _, prefix := range prefixes {
		if prefix == "SUMPTER_" {
			found = true
			break
		}
	}
	if !found {
		t.Error("SUMPTER_ prefix should be detected")
	}
}

func TestPIIRedaction(t *testing.T) {
	tests := []struct {
		key      string
		value    string
		expected string
	}{
		{"HOME", "/Users/test", "/Users/test"},
		{"PWD", "/secret/path", "***redacted***"},
		{"PATH", "/usr/bin", "/usr/bin"},
		{"API_KEY", "secret123", "***redacted***"},
		{"TOKEN", "token456", "***redacted***"},
		{"SECRET_KEY", "secret789", "***redacted***"},
		{"PASSWORD", "mypassword", "***redacted***"},
		{"XML_CATALOG", "/path/to/catalog", "***redacted***"},
		{"DATABASE_URL", "postgres://user:pass@host/db", "***redacted***"},
		{"SESSION_KEY", "session123", "***redacted***"},
		{"AUTH_TOKEN", "auth456", "***redacted***"},
		{"CERTIFICATE", "cert789", "***redacted***"},
		{"PRIVATE_KEY", "key123", "***redacted***"},
		{"JWT_TOKEN", "jwt456", "***redacted***"},
		{"BEARER_TOKEN", "bearer789", "***redacted***"},
		{"API_SECRET", "apisecret123", "***redacted***"},
		{"CREDENTIAL", "cred456", "***redacted***"},
		{"KEY", "key789", "***redacted***"},
		{"NORMAL_VAR", "normalvalue", "normalvalue"},
		{"USER", "testuser", "testuser"},
		{"SHELL", "/bin/bash", "/bin/bash"},
		{"TERM", "xterm", "xterm"},
		{"LANG", "en_US.UTF-8", "en_US.UTF-8"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := maybeRedact(tt.key, tt.value)
			if result != tt.expected {
				t.Errorf("maybeRedact(%s, %s) = %s; expected %s", tt.key, tt.value, result, tt.expected)
			}
		})
	}
}

func TestCollectSystemInfo(t *testing.T) {
	info := collectSystemInfo()

	// Test that required fields are populated
	if info.OS == "" {
		t.Error("OS should not be empty")
	}
	if info.Architecture == "" {
		t.Error("Architecture should not be empty")
	}
	if info.GoVersion == "" {
		t.Error("GoVersion should not be empty")
	}
	if info.NumCPU <= 0 {
		t.Error("NumCPU should be greater than 0")
	}
	if info.Hostname == "" {
		t.Error("Hostname should not be empty")
	}
	if info.WorkingDir == "" {
		t.Error("WorkingDir should not be empty")
	}
	if info.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}

	// Test that timestamp is recent (within last minute)
	if time.Since(info.Timestamp) > time.Minute {
		t.Error("Timestamp should be recent")
	}
}

func TestCollectXMLCapabilities(t *testing.T) {
	caps := collectXMLCapabilities()

	if !caps.StreamingSupported {
		t.Error("StreamingSupported should be true")
	}
	if len(caps.Encodings) == 0 {
		t.Error("Encodings should not be empty")
	}
	if caps.MaxMemoryTarget == "" {
		t.Error("MaxMemoryTarget should not be empty")
	}
	if len(caps.SupportedOutputs) == 0 {
		t.Error("SupportedOutputs should not be empty")
	}

	// Test specific expected values
	expectedEncodings := []string{"UTF-8", "UTF-16", "ISO-8859-1", "Windows-1252"}
	if len(caps.Encodings) != len(expectedEncodings) {
		t.Errorf("Expected %d encodings, got %d", len(expectedEncodings), len(caps.Encodings))
	}
	for i, expected := range expectedEncodings {
		if i < len(caps.Encodings) && caps.Encodings[i] != expected {
			t.Errorf("Expected encoding %s at index %d, got %s", expected, i, caps.Encodings[i])
		}
	}

	if caps.MaxMemoryTarget != "<50MB RSS" {
		t.Errorf("Expected MaxMemoryTarget '<50MB RSS', got '%s'", caps.MaxMemoryTarget)
	}

	expectedOutputs := []string{"JSON", "NDJSON", "Parquet"}
	if len(caps.SupportedOutputs) != len(expectedOutputs) {
		t.Errorf("Expected %d outputs, got %d", len(expectedOutputs), len(caps.SupportedOutputs))
	}
	for i, expected := range expectedOutputs {
		if i < len(caps.SupportedOutputs) && caps.SupportedOutputs[i] != expected {
			t.Errorf("Expected output %s at index %d, got %s", expected, i, caps.SupportedOutputs[i])
		}
	}
}

func TestEnvInfoXMLJSONMatchesSchema(t *testing.T) {
	var buf bytes.Buffer

	cmd := newEnvInfoXMLCommand()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("xml command failed: %v", err)
	}

	var data interface{}
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	schemaBytes, err := os.ReadFile("../../../schemas/envinfo/v0.1.0/xml.schema.json")
	if err != nil {
		t.Fatalf("read XML envinfo schema: %v", err)
	}

	result, err := goneatschema.ValidateFromBytes(schemaBytes, data)
	if err != nil {
		t.Fatalf("validate XML envinfo schema: %v", err)
	}
	if !result.Valid {
		t.Fatalf("envinfo xml JSON failed schema validation: %v", result.Errors)
	}
}

func TestCollectEnvironmentDataUsesXMLCapabilitySource(t *testing.T) {
	data, err := collectEnvironmentData(false, "", false, false, true)
	if err != nil {
		t.Fatalf("collectEnvironmentData: %v", err)
	}

	want := collectXMLCapabilities()
	if data.XML.MaxMemoryTarget != want.MaxMemoryTarget {
		t.Fatalf("MaxMemoryTarget = %q, want %q", data.XML.MaxMemoryTarget, want.MaxMemoryTarget)
	}
	if strings.Join(data.XML.SupportedOutputs, ",") != strings.Join(want.SupportedOutputs, ",") {
		t.Fatalf("SupportedOutputs = %v, want %v", data.XML.SupportedOutputs, want.SupportedOutputs)
	}
	if strings.Join(data.XML.Encodings, ",") != strings.Join(want.Encodings, ",") {
		t.Fatalf("Encodings = %v, want %v", data.XML.Encodings, want.Encodings)
	}
}

func TestCollectEnvironmentVariables(t *testing.T) {
	// Set up test environment variables
	testEnvVars := map[string]string{
		"TEST_VAR1":    "value1",
		"TEST_VAR2":    "value2",
		"SUMPTER_TEST": "sumpter_value",
		"HOME":         "/test/home",
		"USER":         "testuser",
	}

	// Set environment variables
	for key, value := range testEnvVars {
		_ = os.Setenv(key, value)
		defer func(k string) { _ = os.Unsetenv(k) }(key)
	}

	tests := []struct {
		name     string
		all      bool
		filter   string
		expected map[string]string
	}{
		{
			name:     "all=false, no filter",
			all:      false,
			filter:   "",
			expected: map[string]string{"HOME": "/test/home", "USER": "testuser"},
		},
		{
			name:     "all=true, no filter",
			all:      true,
			filter:   "",
			expected: testEnvVars,
		},
		{
			name:     "all=false, filter TEST",
			all:      false,
			filter:   "TEST",
			expected: map[string]string{"TEST_VAR1": "value1", "TEST_VAR2": "value2", "SUMPTER_TEST": "sumpter_value"},
		},
		{
			name:     "all=true, filter SUMPTER",
			all:      true,
			filter:   "SUMPTER",
			expected: map[string]string{"SUMPTER_TEST": "sumpter_value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectEnvironmentVariables(tt.all, tt.filter)

			// Check that all expected variables are present
			for key, expectedValue := range tt.expected {
				if actualValue, exists := result[key]; !exists {
					t.Errorf("Expected variable %s not found", key)
				} else if actualValue != expectedValue {
					t.Errorf("Variable %s: expected %s, got %s", key, expectedValue, actualValue)
				}
			}

			// Check that no unexpected variables are present (for filtered results)
			if tt.filter != "" {
				for key := range result {
					if !strings.Contains(strings.ToLower(key), strings.ToLower(tt.filter)) {
						t.Errorf("Unexpected variable %s found in filtered result", key)
					}
				}
			}
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		slice    []string
		str      string
		expected bool
	}{
		{[]string{"a", "b", "c"}, "b", true},
		{[]string{"a", "b", "c"}, "d", false},
		{[]string{}, "a", false},
		{[]string{"test"}, "test", true},
		{[]string{"Test", "TEST"}, "test", false}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("contains(%v, %s)", tt.slice, tt.str), func(t *testing.T) {
			result := contains(tt.slice, tt.str)
			if result != tt.expected {
				t.Errorf("contains(%v, %s) = %v; expected %v", tt.slice, tt.str, result, tt.expected)
			}
		})
	}
}

// TestResourceCleanupPatterns demonstrates best practices for resource management in tests
func TestResourceCleanupPatterns(t *testing.T) {
	t.Run("temporary directory cleanup", func(t *testing.T) {
		// Create temporary directory
		tempDir, err := os.MkdirTemp("", "sumpter-test-cleanup-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}

		// Ensure cleanup happens even if test fails
		defer func() {
			if err := os.RemoveAll(tempDir); err != nil {
				t.Logf("Warning: failed to cleanup %s: %v", tempDir, err)
			}
		}()

		// Verify directory exists
		if _, err := os.Stat(tempDir); os.IsNotExist(err) {
			t.Error("Temporary directory should exist")
		}

		// Create a test file in the directory
		testFile := filepath.Join(tempDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		// Verify file exists
		if _, err := os.Stat(testFile); os.IsNotExist(err) {
			t.Error("Test file should exist")
		}
	})

	t.Run("environment variable cleanup", func(t *testing.T) {
		// Save original value
		originalValue := os.Getenv("TEST_CLEANUP_VAR")
		defer func() { _ = os.Setenv("TEST_CLEANUP_VAR", originalValue) }() // Restore original

		// Set test value
		testValue := "test-value-123"
		_ = os.Setenv("TEST_CLEANUP_VAR", testValue)

		// Verify value is set
		if actual := os.Getenv("TEST_CLEANUP_VAR"); actual != testValue {
			t.Errorf("Expected %s, got %s", testValue, actual)
		}

		// Value will be automatically restored by defer
	})

	t.Run("parallel safe temporary files", func(t *testing.T) {
		t.Parallel() // This test can run in parallel

		// Use t.TempDir() for automatic cleanup (Go 1.25+)
		tempDir := t.TempDir()

		// Create unique test file for this parallel test
		testFile := filepath.Join(tempDir, "parallel-test.txt")
		content := "parallel test content"

		if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create parallel test file: %v", err)
		}

		// Verify content
		data, err := os.ReadFile(testFile)
		if err != nil {
			t.Fatalf("Failed to read test file: %v", err)
		}

		if string(data) != content {
			t.Errorf("Expected content %s, got %s", content, string(data))
		}
	})

	t.Run("multiple resource cleanup", func(t *testing.T) {
		// Create multiple resources that need cleanup
		tempDir1, err := os.MkdirTemp("", "sumpter-multi-1-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir 1: %v", err)
		}

		tempDir2, err := os.MkdirTemp("", "sumpter-multi-2-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir 2: %v", err)
		}

		// Save original environment variables
		origEnv1 := os.Getenv("TEST_MULTI_1")
		origEnv2 := os.Getenv("TEST_MULTI_2")

		// Set test environment variables
		_ = os.Setenv("TEST_MULTI_1", "value1")
		_ = os.Setenv("TEST_MULTI_2", "value2")

		// Cleanup function that handles all resources
		cleanup := func() {
			// Cleanup directories
			_ = os.RemoveAll(tempDir1)
			_ = os.RemoveAll(tempDir2)

			// Restore environment variables
			_ = os.Setenv("TEST_MULTI_1", origEnv1)
			_ = os.Setenv("TEST_MULTI_2", origEnv2)
		}
		defer cleanup()

		// Test that resources exist
		if _, err := os.Stat(tempDir1); os.IsNotExist(err) {
			t.Error("Temp dir 1 should exist")
		}
		if _, err := os.Stat(tempDir2); os.IsNotExist(err) {
			t.Error("Temp dir 2 should exist")
		}

		// Test environment variables
		if os.Getenv("TEST_MULTI_1") != "value1" {
			t.Error("TEST_MULTI_1 should be set to value1")
		}
		if os.Getenv("TEST_MULTI_2") != "value2" {
			t.Error("TEST_MULTI_2 should be set to value2")
		}
	})
}

// TestParallelSafety demonstrates parallel test execution patterns
func TestParallelSafety(t *testing.T) {
	t.Run("parallel subtests", func(t *testing.T) {
		// Run multiple subtests in parallel
		testCases := []struct {
			name  string
			value int
		}{
			{"test1", 1},
			{"test2", 2},
			{"test3", 3},
			{"test4", 4},
			{"test5", 5},
		}

		for _, tc := range testCases {
			tc := tc // Capture loop variable
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel() // Run this subtest in parallel

				// Simulate some work
				result := tc.value * 2

				if result != tc.value*2 {
					t.Errorf("Expected %d, got %d", tc.value*2, result)
				}
			})
		}
	})

	t.Run("shared resource parallel access", func(t *testing.T) {
		t.Parallel()

		// This test demonstrates safe parallel access to shared resources
		// In real scenarios, you might use mutexes or channels for synchronization

		tempDir := t.TempDir()
		counterFile := filepath.Join(tempDir, "counter.txt")

		// Initialize counter
		if err := os.WriteFile(counterFile, []byte("0"), 0644); err != nil {
			t.Fatalf("Failed to create counter file: %v", err)
		}

		// In a real scenario, you'd use proper synchronization here
		// This is just a demonstration of the pattern
		t.Logf("Parallel test using shared resource: %s", counterFile)
	})
}

// TestErrorHandling demonstrates comprehensive error handling patterns
func TestErrorHandling(t *testing.T) {
	t.Run("invalid JSON output handling", func(t *testing.T) {
		// Test that invalid data doesn't break JSON marshaling
		var buf bytes.Buffer

		cmd := NewEnvInfoCommand()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"--json"})

		// This should not panic even if there are issues
		err := cmd.Execute()
		if err != nil {
			t.Logf("Command returned error (expected): %v", err)
		}

		output := buf.String()
		if output == "" {
			t.Error("Expected some output even with errors")
		}
	})

	t.Run("network interface error handling", func(t *testing.T) {
		// Test that network interface errors are handled gracefully
		interfaces, err := getNetworkInterfaces()

		// Should not panic, even if network interfaces can't be retrieved
		if err != nil {
			t.Logf("Network interface error (expected in some environments): %v", err)
		}

		// interfaces can be nil or empty, but should not cause panic
		_ = interfaces
	})

	t.Run("external IP error handling", func(t *testing.T) {
		// Test external IP fetching with timeout/error handling
		ip, err := getExternalIP()

		// Should handle network errors gracefully
		if err != nil {
			t.Logf("External IP error (expected in offline environments): %v", err)
		}

		// IP can be empty string on error, but should not panic
		_ = ip
	})

	t.Run("path resolution error handling", func(t *testing.T) {
		// Test path resolution with invalid inputs
		paths, err := config.ResolvePaths("", "")

		// Should handle path resolution errors gracefully
		if err != nil {
			t.Logf("Path resolution error: %v", err)
		}

		// Note: ResolvePaths may return nil on error depending on implementation
		// This is acceptable behavior - the error is more important than the return value
		_ = paths
	})

	t.Run("malformed environment variables", func(t *testing.T) {
		// Test handling of malformed environment variables
		// This simulates edge cases in environment parsing

		// Save original environ
		originalEnviron := os.Environ()
		defer func() {
			// Restore original environment (simplified)
			os.Clearenv()
			for _, env := range originalEnviron {
				pair := strings.SplitN(env, "=", 2)
				if len(pair) == 2 {
					_ = os.Setenv(pair[0], pair[1])
				}
			}
		}()

		// Clear environment and set some test values
		os.Clearenv()
		_ = os.Setenv("VALID_VAR", "valid_value")
		_ = os.Setenv("EMPTY_VAR", "")
		_ = os.Setenv("VAR_WITH_EQUALS", "value=with=equals")

		// Test that collectEnvironmentVariables handles these gracefully
		vars := collectEnvironmentVariables(true, "")

		if len(vars) == 0 {
			t.Error("Should collect some environment variables")
		}

		// Verify specific values
		if vars["VALID_VAR"] != "valid_value" {
			t.Errorf("VALID_VAR should be 'valid_value', got '%s'", vars["VALID_VAR"])
		}

		if vars["EMPTY_VAR"] != "" {
			t.Errorf("EMPTY_VAR should be empty, got '%s'", vars["EMPTY_VAR"])
		}

		if vars["VAR_WITH_EQUALS"] != "value=with=equals" {
			t.Errorf("VAR_WITH_EQUALS should handle equals, got '%s'", vars["VAR_WITH_EQUALS"])
		}
	})
}

// TestEdgeCases covers various edge cases and boundary conditions
func TestEdgeCases(t *testing.T) {
	t.Run("empty environment", func(t *testing.T) {
		// Save original environment
		originalEnviron := os.Environ()
		defer func() {
			// Restore environment
			os.Clearenv()
			for _, env := range originalEnviron {
				pair := strings.SplitN(env, "=", 2)
				if len(pair) == 2 {
					_ = os.Setenv(pair[0], pair[1])
				}
			}
		}()

		// Clear all environment variables
		os.Clearenv()

		// Test that functions handle empty environment gracefully
		vars := collectEnvironmentVariables(true, "")
		if vars == nil {
			t.Error("collectEnvironmentVariables should return empty map, not nil")
		}

		info := collectSystemInfo()
		if info.OS == "" {
			t.Error("collectSystemInfo should still populate OS even with empty env")
		}
	})

	t.Run("very long environment variable values", func(t *testing.T) {
		// Test handling of very long environment variable values
		longValue := strings.Repeat("x", 10000) // 10KB string
		_ = os.Setenv("TEST_LONG_VAR", longValue)
		defer func() { _ = os.Unsetenv("TEST_LONG_VAR") }()

		vars := collectEnvironmentVariables(true, "TEST_LONG")
		if vars["TEST_LONG_VAR"] != longValue {
			t.Error("Should handle very long environment variable values")
		}
	})

	t.Run("special characters in environment variables", func(t *testing.T) {
		specialValue := "value with spaces & special chars: !@#$%^&*()"
		_ = os.Setenv("TEST_SPECIAL_VAR", specialValue)
		defer func() { _ = os.Unsetenv("TEST_SPECIAL_VAR") }()

		vars := collectEnvironmentVariables(true, "TEST_SPECIAL")
		if vars["TEST_SPECIAL_VAR"] != specialValue {
			t.Errorf("Should handle special characters, expected '%s', got '%s'", specialValue, vars["TEST_SPECIAL_VAR"])
		}
	})

	t.Run("unicode in environment variables", func(t *testing.T) {
		unicodeValue := "测试 🚀 Unicode 值"
		_ = os.Setenv("TEST_UNICODE_VAR", unicodeValue)
		defer func() { _ = os.Unsetenv("TEST_UNICODE_VAR") }()

		vars := collectEnvironmentVariables(true, "TEST_UNICODE")
		if vars["TEST_UNICODE_VAR"] != unicodeValue {
			t.Errorf("Should handle Unicode characters, expected '%s', got '%s'", unicodeValue, vars["TEST_UNICODE_VAR"])
		}
	})
}

// TestJSONValidation tests JSON output validation and schema compliance
func TestJSONValidation(t *testing.T) {
	t.Run("system subcommand JSON validation", func(t *testing.T) {
		var buf bytes.Buffer

		cmd := newEnvInfoSystemCommand()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"--json"})

		err := cmd.Execute()
		if err != nil {
			t.Fatalf("System command failed: %v", err)
		}

		output := buf.String()
		if output == "" {
			t.Fatal("Expected JSON output")
		}

		// Parse JSON
		var data SystemInfo
		if err := json.Unmarshal([]byte(output), &data); err != nil {
			t.Fatalf("Invalid JSON: %v", err)
		}

		// Validate required fields
		if data.OS == "" {
			t.Error("OS field should not be empty")
		}
		if data.Architecture == "" {
			t.Error("Architecture field should not be empty")
		}
		if data.GoVersion == "" {
			t.Error("GoVersion field should not be empty")
		}
		if data.NumCPU <= 0 {
			t.Error("NumCPU should be greater than 0")
		}
		if data.Hostname == "" {
			t.Error("Hostname should not be empty")
		}
		if data.WorkingDir == "" {
			t.Error("WorkingDir should not be empty")
		}
		if data.Timestamp.IsZero() {
			t.Error("Timestamp should not be zero")
		}
	})

	t.Run("main command JSON validation", func(t *testing.T) {
		var buf bytes.Buffer

		cmd := NewEnvInfoCommand()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"--json"})

		err := cmd.Execute()
		if err != nil {
			t.Fatalf("Main command failed: %v", err)
		}

		output := buf.String()
		if output == "" {
			t.Fatal("Expected JSON output")
		}

		// Parse JSON
		var data EnvData
		if err := json.Unmarshal([]byte(output), &data); err != nil {
			t.Fatalf("Invalid JSON: %v", err)
		}

		// Validate structure
		if data.System.OS == "" {
			t.Error("System.OS should not be empty")
		}
		// Application paths may be empty if path resolution fails
		// This is acceptable behavior
		if data.Stats.TotalVars < 0 {
			t.Error("Stats.TotalVars should not be negative")
		}
	})
}

// TestWriteHelpers tests the writeFprintf and writeFprintln helper functions
func TestWriteHelpers(t *testing.T) {
	t.Run("writeFprintf basic", func(t *testing.T) {
		var buf bytes.Buffer
		err := writeFprintf(&buf, "Hello %s", "World")
		if err != nil {
			t.Errorf("writeFprintf() error = %v", err)
		}
		if buf.String() != "Hello World" {
			t.Errorf("writeFprintf() = %q, want %q", buf.String(), "Hello World")
		}
	})

	t.Run("writeFprintf with numbers", func(t *testing.T) {
		var buf bytes.Buffer
		err := writeFprintf(&buf, "Count: %d, Float: %.2f", 42, 3.14159)
		if err != nil {
			t.Errorf("writeFprintf() error = %v", err)
		}
		expected := "Count: 42, Float: 3.14"
		if buf.String() != expected {
			t.Errorf("writeFprintf() = %q, want %q", buf.String(), expected)
		}
	})

	t.Run("writeFprintln basic", func(t *testing.T) {
		var buf bytes.Buffer
		err := writeFprintln(&buf, "Hello", "World")
		if err != nil {
			t.Errorf("writeFprintln() error = %v", err)
		}
		expected := "Hello World\n"
		if buf.String() != expected {
			t.Errorf("writeFprintln() = %q, want %q", buf.String(), expected)
		}
	})

	t.Run("writeFprintln with single arg", func(t *testing.T) {
		var buf bytes.Buffer
		err := writeFprintln(&buf, "Test")
		if err != nil {
			t.Errorf("writeFprintln() error = %v", err)
		}
		expected := "Test\n"
		if buf.String() != expected {
			t.Errorf("writeFprintln() = %q, want %q", buf.String(), expected)
		}
	})

	t.Run("writeFprintln with no args", func(t *testing.T) {
		var buf bytes.Buffer
		err := writeFprintln(&buf)
		if err != nil {
			t.Errorf("writeFprintln() error = %v", err)
		}
		expected := "\n"
		if buf.String() != expected {
			t.Errorf("writeFprintln() = %q, want %q", buf.String(), expected)
		}
	})
}

const wantEnvInfoRoutes = `Input route support (recipe/selector constraints apply)
==================================================
Extract files    | xml, json, ndjson
Inspect          | xml, json
Record analysis  | xml
Record indexes   | xml, json; uncompressed source
Extract-multi    | xml
JSON streaming/indexed and NDJSON extraction require record-scoped recipes
with eligible selectors and outputs. NDJSON is not an inspect or index input;
JSON/NDJSON record analysis is refused. Uncompressed source means the document,
not the index store (JSON or optional seekable-zstd).

`

const wantHumanXMLTarget = "<50MB RSS (XML input-tokenization design target; not measured per-run or a JSON bound)"

func envInfoPresentationFixture() *EnvData {
	return &EnvData{
		System: SystemInfo{OS: "test", Architecture: "test", GoVersion: "test", NumCPU: 1,
			Hostname: "test", WorkingDir: ".", Timestamp: time.Unix(0, 0).UTC()},
		Variables: map[string]string{"SUMPTER_TEST": "value"},
	}
}

func TestEnvInfoRootHumanRoutes(t *testing.T) {
	for _, xml := range []bool{false, true} {
		t.Run(fmt.Sprintf("xml=%t", xml), func(t *testing.T) {
			data := envInfoPresentationFixture()
			if xml {
				data.XML = collectXMLCapabilities()
			}
			var out bytes.Buffer
			cmd := NewEnvInfoCommand()
			cmd.SetOut(&out)
			if err := outputHumanReadable(cmd, data, false); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			if strings.Count(text, wantEnvInfoRoutes) != 1 {
				t.Fatalf("expected route section exactly once:\n%s", text)
			}
			if strings.Index(text, "Timestamp") > strings.Index(text, "Input route support") ||
				strings.Index(text, "Input route support") > strings.Index(text, "Application Environment") {
				t.Fatalf("route section is not after system info: %s", text)
			}
			if got := strings.Count(text, wantHumanXMLTarget); got != boolToCount(xml) {
				t.Fatalf("XML target annotations = %d, want xml=%t", got, xml)
			}
			if xml && data.XML.MaxMemoryTarget != "<50MB RSS" {
				t.Fatal("human renderer mutated machine target")
			}
		})
	}
}

func boolToCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestEnvInfoPresentationModes(t *testing.T) {
	t.Setenv("SUMPTER_HOME", t.TempDir())
	t.Setenv("SUMPTER_WORKDIR", t.TempDir())
	t.Setenv("SUMPTER_PRESENTATION_FIXTURE", "value")
	for _, mode := range []string{"human", "json", "export", "json+export"} {
		for _, xml := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/xml=%t", mode, xml), func(t *testing.T) {
				args := []string{"--all", "--filter", "SUMPTER_PRESENTATION_FIXTURE"}
				if xml {
					args = append(args, "--xml")
				}
				if strings.Contains(mode, "json") {
					args = append(args, "--json")
				}
				if strings.Contains(mode, "export") {
					args = append(args, "--export")
				}
				var out bytes.Buffer
				cmd := NewEnvInfoCommand()
				cmd.SetOut(&out)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				text := out.String()
				if got := strings.Count(text, "Input route support"); got != boolToCount(mode == "human") {
					t.Fatalf("route section count = %d for %s", got, mode)
				}
				if mode != "human" && strings.Contains(text, "design target") {
					t.Fatalf("human annotation leaked to %s", mode)
				}
				if strings.Contains(mode, "json") {
					var payload map[string]json.RawMessage
					if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					// These struct-valued sections are emitted even when empty;
					// pin the existing root key set, not new capability fields.
					wantKeys := []string{"system", "network", "variables", "stats", "xml", "application"}
					if len(payload) != len(wantKeys) {
						t.Fatalf("unexpected root key set: %v", payload)
					}
					for _, key := range wantKeys {
						if _, ok := payload[key]; !ok {
							t.Fatalf("missing root key %s", key)
						}
					}
					var caps XMLCapabilities
					if err := json.Unmarshal(payload["xml"], &caps); err != nil {
						t.Fatal(err)
					}
					wantCaps := XMLCapabilities{}
					if xml {
						wantCaps = collectXMLCapabilities()
					}
					if !reflect.DeepEqual(caps, wantCaps) {
						t.Fatalf("machine XML values changed: %+v", caps)
					}
					var xmlKeys map[string]json.RawMessage
					if err := json.Unmarshal(payload["xml"], &xmlKeys); err != nil {
						t.Fatal(err)
					}
					if len(xmlKeys) != 4 {
						t.Fatalf("new machine XML keys: %v", xmlKeys)
					}
				} else if mode == "export" && !strings.Contains(text, `export SUMPTER_PRESENTATION_FIXTURE="value"`) {
					t.Fatal("existing export assignment missing")
				}
			})
		}
	}
}

func TestEnvInfoSinglePurposePresentation(t *testing.T) {
	t.Setenv("SUMPTER_HOME", t.TempDir())
	t.Setenv("SUMPTER_WORKDIR", t.TempDir())
	for _, name := range []string{"system", "paths", "vars", "xml", "network"} {
		for _, machine := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", name, machine), func(t *testing.T) {
				var out bytes.Buffer
				cmd := NewEnvInfoCommand()
				cmd.SetOut(&out)
				cmd.SetErr(io.Discard)
				args := []string{name}
				if machine {
					args = append(args, "--json")
				}
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				text := out.String()
				if strings.Contains(text, "Input route support") || strings.Contains(text, "record-scoped recipes") {
					t.Fatalf("route section leaked to %s", name)
				}
				if name == "xml" && !machine {
					if strings.Count(text, wantHumanXMLTarget) != 1 {
						t.Fatalf("missing XML target qualification: %s", text)
					}
				} else if strings.Contains(text, "design target") {
					t.Fatalf("XML human annotation leaked to %s/json=%t", name, machine)
				}
				if name == "xml" && machine {
					want := `{
  "streamingSupported": true,
  "encodings": [
    "UTF-8",
    "UTF-16",
    "ISO-8859-1",
    "Windows-1252"
  ],
  "maxMemoryTarget": "\u003c50MB RSS",
  "supportedOutputs": [
    "JSON",
    "NDJSON",
    "Parquet"
  ]
}
`
					if text != want {
						t.Fatalf("XML JSON byte contract changed:\n%s", text)
					}
				}
			})
		}
	}
}

type envInfoPresentationWriter struct {
	writes []string
	failAt int
	err    error
}

func (w *envInfoPresentationWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, string(p))
	if len(w.writes) == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

func TestEnvInfoRouteSectionWriteFailures(t *testing.T) {
	data := envInfoPresentationFixture()
	data.XML = collectXMLCapabilities()
	cmd := NewEnvInfoCommand()
	var recorded envInfoPresentationWriter
	cmd.SetOut(&recorded)
	if err := outputHumanReadable(cmd, data, false); err != nil {
		t.Fatal(err)
	}
	first, count := -1, strings.Count(wantEnvInfoRoutes, "\n")
	for i, write := range recorded.writes {
		if strings.HasPrefix(write, "Input route support") {
			first = i
			break
		}
	}
	if first < 0 {
		t.Fatal("route section never rendered")
	}
	for i := 0; i < count; i++ {
		t.Run(fmt.Sprintf("line-%d", i+1), func(t *testing.T) {
			wantErr := errors.New("test writer failure")
			writer := &envInfoPresentationWriter{failAt: first + i + 1, err: wantErr}
			cmd.SetOut(writer)
			err := outputHumanReadable(cmd, data, false)
			if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "input route support") {
				t.Fatalf("error = %v, want route-section writer failure", err)
			}
			if len(writer.writes) != writer.failAt {
				t.Fatal("renderer kept writing after error")
			}
		})
	}
}

func TestEnvInfoHumanXMLTargetWriteFailures(t *testing.T) {
	data := envInfoPresentationFixture()
	data.XML = collectXMLCapabilities()
	renderers := map[string]func(*cobra.Command) error{
		"root": func(cmd *cobra.Command) error { return outputHumanReadable(cmd, data, false) },
		"xml":  func(cmd *cobra.Command) error { return outputXMLCapabilities(cmd, data.XML) },
	}
	for name, render := range renderers {
		t.Run(name, func(t *testing.T) {
			cmd := &cobra.Command{}
			var recorded envInfoPresentationWriter
			cmd.SetOut(&recorded)
			if err := render(cmd); err != nil {
				t.Fatal(err)
			}
			for i, text := range recorded.writes {
				if strings.Contains(text, wantHumanXMLTarget) {
					wantErr := errors.New("test target writer failure")
					writer := &envInfoPresentationWriter{failAt: i + 1, err: wantErr}
					cmd.SetOut(writer)
					if err := render(cmd); !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "memory target") {
						t.Fatalf("target write error = %v", err)
					}
					return
				}
			}
			t.Fatal("qualified target not written")
		})
	}
}
