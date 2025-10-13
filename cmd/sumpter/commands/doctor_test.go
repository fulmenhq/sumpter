package commands

import (
	"bufio"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestDoctorCommand(t *testing.T) {
	cmd := NewDoctorCommand()

	if cmd == nil {
		t.Fatal("expected doctor command, got nil")
	}

	if cmd.Use != "doctor" {
		t.Errorf("Use = %q, want 'doctor'", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("Short description is empty")
	}

	// Verify command structure
	if len(cmd.Aliases) > 0 {
		t.Logf("Doctor command aliases: %v", cmd.Aliases)
	}
}

func TestDoctorSubcommands(t *testing.T) {
	tests := []struct {
		name string
		use  string
	}{
		{"setup subcommand", "setup"},
		{"check subcommand", "check"},
		{"config subcommand", "config"},
	}

	doctorCmd := NewDoctorCommand()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := false
			for _, subCmd := range doctorCmd.Commands() {
				if subCmd.Use == tt.use || (len(subCmd.Use) > len(tt.use) && subCmd.Use[:len(tt.use)] == tt.use) {
					found = true
					if subCmd.Short == "" {
						t.Errorf("subcommand %q has empty Short description", tt.use)
					}
					break
				}
			}
			if !found {
				t.Errorf("subcommand %q not found", tt.use)
			}
		})
	}
}

func TestDetectOptimalHomeDir(t *testing.T) {
	homeDir, err := detectOptimalHomeDir()
	if err != nil {
		t.Fatalf("detectOptimalHomeDir() error = %v", err)
	}

	if homeDir == "" {
		t.Error("detectOptimalHomeDir() returned empty string")
	}
}

func TestDetectShellType(t *testing.T) {
	// Save original SHELL env var
	originalShell := os.Getenv("SHELL")
	defer func() {
		if originalShell != "" {
			_ = os.Setenv("SHELL", originalShell)
		} else {
			_ = os.Unsetenv("SHELL")
		}
	}()

	tests := []struct {
		name     string
		shellEnv string
		want     string
	}{
		{"zsh shell", "/bin/zsh", "zsh"},
		{"bash shell", "/bin/bash", "bash"},
		{"fish shell", "/usr/local/bin/fish", "fish"},
		{"unknown shell", "/bin/unknown", "bash"}, // defaults to bash
		{"empty shell", "", "bash"},               // defaults to bash
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shellEnv != "" {
				_ = os.Setenv("SHELL", tt.shellEnv)
			} else {
				_ = os.Unsetenv("SHELL")
			}

			got := detectShellType()
			if got != tt.want {
				t.Errorf("detectShellType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShowOSSetupInstructions(t *testing.T) {
	homeDir := "/test/home"

	err := showOSSetupInstructions(homeDir)
	if err != nil {
		t.Errorf("showOSSetupInstructions() error = %v", err)
	}

	// Function should work for all OS types
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		// These are explicitly handled
	default:
		// Generic fallback should work
	}
}

func TestGenerateSetupScript(t *testing.T) {
	tests := []struct {
		name       string
		shellType  string
		customHome string
		dryRun     bool
		wantErr    bool
	}{
		{"bash script", "bash", "/test/home", false, false},
		{"zsh script", "zsh", "/test/home", false, false},
		{"fish script", "fish", "/test/home", false, false},
		{"powershell script", "powershell", "/test/home", false, false},
		{"dry run", "bash", "/test/home", true, false},
		{"unsupported shell", "unknown", "/test/home", false, true},
		{"detect shell type", "", "/test/home", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := generateSetupScript(tt.shellType, tt.customHome, tt.dryRun)
			if (err != nil) != tt.wantErr {
				t.Errorf("generateSetupScript() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Should not panic for existing directory
	checkDir("TestDir", tmpDir)

	// Should not panic for non-existent directory
	checkDir("NonExistent", "/nonexistent/path")
}

func TestGenerateRetrieveSecEdgarConfig(t *testing.T) {
	companyName := "Test Company"
	contactEmail := "test@example.com"
	rateLimit := 8
	burstLimit := 5

	config := generateRetrieveSecEdgarConfig(companyName, contactEmail, rateLimit, burstLimit)

	if config == "" {
		t.Error("generateRetrieveSecEdgarConfig() returned empty string")
	}

	// Check that config contains expected values
	if !strings.Contains(config, companyName) {
		t.Errorf("config does not contain company name %q", companyName)
	}

	if !strings.Contains(config, contactEmail) {
		t.Errorf("config does not contain contact email %q", contactEmail)
	}

	if !strings.Contains(config, "version: \"retrieve/v0.1.0\"") {
		t.Error("config does not contain version")
	}

	if !strings.Contains(config, "finance:") {
		t.Error("config does not contain finance realm")
	}
}

func TestPromptRateLimitWithScanner(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"valid rate 5", "5\n", 5},
		{"valid rate 8", "8\n", 8},
		{"valid rate 1", "1\n", 1},
		{"empty input defaults", "\n", 8},
		{"invalid string", "abc\n", 8},
		{"out of range high", "10\n", 8},
		{"out of range low", "0\n", 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(tt.input))
			got := promptRateLimitWithScanner(scanner)
			if got != tt.want {
				t.Errorf("promptRateLimitWithScanner() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPromptBurstLimitWithScanner(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"valid burst 10", "10\n", 10},
		{"valid burst 1", "1\n", 1},
		{"empty input defaults", "\n", 5},
		{"invalid string", "xyz\n", 5},
		{"negative value", "-1\n", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(tt.input))
			got := promptBurstLimitWithScanner(scanner)
			if got != tt.want {
				t.Errorf("promptBurstLimitWithScanner() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPromptConfirmationWithScanner(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"yes lowercase", "y\n", true},
		{"yes uppercase", "Y\n", true},
		{"yes full word", "yes\n", true},
		{"yes full word uppercase", "YES\n", true},
		{"no lowercase", "n\n", false},
		{"no uppercase", "N\n", false},
		{"empty input", "\n", false},
		{"invalid input", "maybe\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(tt.input))
			got := promptConfirmationWithScanner(scanner, "Test message")
			if got != tt.want {
				t.Errorf("promptConfirmationWithScanner() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDoctorConfigSubcommands(t *testing.T) {
	doctorCmd := NewDoctorCommand()

	var configCmd *testing.T
	for _, subCmd := range doctorCmd.Commands() {
		if subCmd.Use == "config" {
			// Check config subcommands
			subCmds := subCmd.Commands()
			if len(subCmds) == 0 {
				t.Error("config command should have subcommands")
			}

			// Verify specific subcommands exist
			expectedSubs := []string{"list", "setup", "validate"}
			for _, expected := range expectedSubs {
				found := false
				for _, sub := range subCmds {
					if sub.Use == expected || strings.HasPrefix(sub.Use, expected+" ") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("config subcommand %q not found", expected)
				}
			}
			return
		}
	}

	if configCmd == nil {
		t.Error("config subcommand not found")
	}
}

func TestCheckEnvVars(t *testing.T) {
	// Save original values
	originalHome := os.Getenv("SUMPTER_HOME")
	originalWorkdir := os.Getenv("SUMPTER_WORKDIR")
	defer func() {
		if originalHome != "" {
			_ = os.Setenv("SUMPTER_HOME", originalHome)
		} else {
			_ = os.Unsetenv("SUMPTER_HOME")
		}
		if originalWorkdir != "" {
			_ = os.Setenv("SUMPTER_WORKDIR", originalWorkdir)
		} else {
			_ = os.Unsetenv("SUMPTER_WORKDIR")
		}
	}()

	tests := []struct {
		name      string
		setHome   bool
		setWork   bool
		homeValue string
		workValue string
	}{
		{"both set", true, true, "/test/home", "/test/work"},
		{"only home set", true, false, "/test/home", ""},
		{"neither set", false, false, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setHome {
				_ = os.Setenv("SUMPTER_HOME", tt.homeValue)
			} else {
				_ = os.Unsetenv("SUMPTER_HOME")
			}

			if tt.setWork {
				_ = os.Setenv("SUMPTER_WORKDIR", tt.workValue)
			} else {
				_ = os.Unsetenv("SUMPTER_WORKDIR")
			}

			// Should not panic
			checkEnvVars()
		})
	}
}

func TestCheckPaths(t *testing.T) {
	// Should not panic even if paths can't be resolved
	checkPaths()
}

func TestCheckConfig(t *testing.T) {
	// Should not panic even if config can't be checked
	checkConfig()
}
