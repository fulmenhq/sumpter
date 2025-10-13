package commands

import (
	"testing"
)

func TestRecipesCommand(t *testing.T) {
	cmd := NewRecipesCommand()

	if cmd == nil {
		t.Fatal("expected recipes command, got nil")
	}

	if cmd.Use != "recipes" {
		t.Errorf("Use = %q, want 'recipes'", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("Short description is empty")
	}

	// Verify command has subcommands
	subCommands := cmd.Commands()
	if len(subCommands) == 0 {
		t.Error("expected subcommands, got none")
	}
}

func TestRecipesSubcommands(t *testing.T) {
	tests := []struct {
		name string
		use  string
	}{
		{"init subcommand", "init"},
		{"retrieve subcommand", "retrieve"},
		{"run subcommand", "run"},
	}

	recipesCmd := NewRecipesCommand()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := false
			for _, subCmd := range recipesCmd.Commands() {
				// Check if Use starts with the command name (handles "command <args>" format)
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
