package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// Build-time variables injected by make build
var (
	Version   = "dev"     // Injected from VERSION file
	BuildTime = "unknown" // Injected at build time
	GitCommit = "unknown" // Injected from git rev-parse
)

// GitStatus represents the current git repository status
type GitStatus struct {
	Branch     string `json:"branch,omitempty"`
	Commit     string `json:"commit,omitempty"`
	CommitFull string `json:"commit_full,omitempty"`
	Clean      bool   `json:"clean"`
	Staged     int    `json:"staged,omitempty"`
	Unstaged   int    `json:"unstaged,omitempty"`
	Untracked  int    `json:"untracked,omitempty"`
	Ahead      int    `json:"ahead,omitempty"`
	Behind     int    `json:"behind,omitempty"`
}

// VersionInfo contains comprehensive version information
type VersionInfo struct {
	Version     string     `json:"version"`
	BuildTime   string     `json:"build_time"`
	GitCommit   string     `json:"git_commit"`
	GoVersion   string     `json:"go_version"`
	Platform    string     `json:"platform"`
	Arch        string     `json:"arch"`
	GitStatus   *GitStatus `json:"git_status,omitempty"`
	Environment string     `json:"environment,omitempty"`
}

func NewVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Display version information (use --extended for build details)",
		Run:   runVersionCommand,
	}

	cmd.Flags().Bool("extended", false, "Show detailed build and git information")
	cmd.Flags().Bool("json", false, "Output version information in JSON format")

	return cmd
}

func runVersionCommand(cmd *cobra.Command, args []string) {
	// Create component logger for version command
	log := logging.Component("version")

	extended, _ := cmd.Flags().GetBool("extended")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	out := cmd.OutOrStdout()

	log.Info("Version command executed",
		zap.Bool("extended", extended),
		zap.Bool("json_output", jsonOutput))

	// Get basic version info
	version := getVersionFromBuild()
	info := VersionInfo{
		Version:   version,
		BuildTime: BuildTime,
		GitCommit: GitCommit,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS,
		Arch:      runtime.GOARCH,
	}

	log.Debug("Basic version info collected",
		zap.String("version", version),
		zap.String("build_time", BuildTime),
		zap.String("git_commit", GitCommit))

	// Add extended information if requested
	if extended || jsonOutput {
		log.Debug("Collecting extended version information")
		info.GitStatus = getCurrentGitStatus()
		info.Environment = getEnvironment()
		log.Debug("Extended version information collected",
			zap.Bool("has_git_status", info.GitStatus != nil),
			zap.String("environment", info.Environment))
	}

	switch {
	case jsonOutput:
		// Output as JSON
		jsonBytes, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintf(out, "Error formatting JSON: %v\n", err)
			return
		}
		_, _ = fmt.Fprintln(out, string(jsonBytes))
	case extended:
		// Extended human-readable output
		_, _ = fmt.Fprintf(out, "Sumpter v%s\n", info.Version)

		if info.GitStatus != nil {
			// Git commit info with dirty flag
			gitInfo := fmt.Sprintf("Git commit: %s", info.GitCommit)
			if len(info.GitStatus.CommitFull) > 8 {
				gitInfo = fmt.Sprintf("Git commit: %s", info.GitStatus.CommitFull[:8])
			}

			if !info.GitStatus.Clean {
				dirtyFlags := []string{}
				if info.GitStatus.Staged > 0 {
					dirtyFlags = append(dirtyFlags, fmt.Sprintf("+%d", info.GitStatus.Staged))
				}
				if info.GitStatus.Unstaged > 0 {
					dirtyFlags = append(dirtyFlags, fmt.Sprintf("~%d", info.GitStatus.Unstaged))
				}
				if info.GitStatus.Untracked > 0 {
					dirtyFlags = append(dirtyFlags, fmt.Sprintf("?%d", info.GitStatus.Untracked))
				}
				if len(dirtyFlags) > 0 {
					gitInfo += fmt.Sprintf("-dirty(%s)", strings.Join(dirtyFlags, ","))
				}
			}
			_, _ = fmt.Fprintln(out, gitInfo)

			// Git branch info with ahead/behind
			if info.GitStatus.Branch != "" {
				branchInfo := fmt.Sprintf("Git branch: %s", info.GitStatus.Branch)
				if info.GitStatus.Ahead > 0 || info.GitStatus.Behind > 0 {
					aheadBehind := []string{}
					if info.GitStatus.Ahead > 0 {
						aheadBehind = append(aheadBehind, fmt.Sprintf("ahead %d", info.GitStatus.Ahead))
					}
					if info.GitStatus.Behind > 0 {
						aheadBehind = append(aheadBehind, fmt.Sprintf("behind %d", info.GitStatus.Behind))
					}
					branchInfo += fmt.Sprintf(" (%s)", strings.Join(aheadBehind, ", "))
				}
				_, _ = fmt.Fprintln(out, branchInfo)
			}
		}

		_, _ = fmt.Fprintf(out, "Build time: %s\n", info.BuildTime)
		_, _ = fmt.Fprintf(out, "Platform: %s/%s\n", info.Platform, info.Arch)
		_, _ = fmt.Fprintf(out, "Go version: %s\n", info.GoVersion)

		if info.Environment != "" {
			_, _ = fmt.Fprintf(out, "Environment: %s\n", info.Environment)
		}
	default:
		// Simple version output
		_, _ = fmt.Fprintf(out, "Sumpter v%s\n", info.Version)
		_, _ = fmt.Fprintf(out, "Go Version: %s\n", info.GoVersion)
		_, _ = fmt.Fprintf(out, "OS/Arch: %s/%s\n", info.Platform, info.Arch)
	}
}

// getVersionFromBuild returns the build-time injected version or falls back to VERSION file
func getVersionFromBuild() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	return getVersion() // Fallback to VERSION file
}

// getCurrentGitStatus gets current git repository status
func getCurrentGitStatus() *GitStatus {
	// Check if we're in a git repository
	if !isGitRepository() {
		return nil
	}

	status := &GitStatus{}

	// Get current branch
	if branch := getGitBranch(); branch != "" {
		status.Branch = branch
	}

	// Get current commit (full and short)
	if commit := getGitCommit(); commit != "" {
		status.CommitFull = commit
		if len(commit) > 8 {
			status.Commit = commit[:8]
		} else {
			status.Commit = commit
		}
	}

	// Get working tree status
	status.Clean = isGitClean()

	// Get detailed status counts
	status.Staged = getGitStatusCount("--cached")
	status.Unstaged = getGitStatusCount("--")
	status.Untracked = getGitUntrackedCount()

	// Get ahead/behind info
	status.Ahead, status.Behind = getGitAheadBehind()

	return status
}

// getEnvironment determines the current environment
func getEnvironment() string {
	if env := os.Getenv("SUMPTER_ENV"); env != "" {
		return env
	}
	if env := os.Getenv("ENV"); env != "" {
		return env
	}
	if env := os.Getenv("NODE_ENV"); env != "" {
		return env
	}

	// Default environment detection
	if _, err := os.Stat("go.mod"); err == nil {
		return "development"
	}

	return "production"
}

// Git utility functions
func isGitRepository() bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func getGitBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	if output, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(output))
	}
	return ""
}

func getGitCommit() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	if output, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(output))
	}
	return ""
}

func isGitClean() bool {
	cmd := exec.Command("git", "status", "--porcelain")
	output, err := cmd.Output()
	return err == nil && len(strings.TrimSpace(string(output))) == 0
}

func getGitStatusCount(flag string) int {
	var cmd *exec.Cmd
	if flag == "--cached" {
		cmd = exec.Command("git", "diff", "--cached", "--name-only")
	} else {
		cmd = exec.Command("git", "diff", "--name-only")
	}

	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}
	return len(lines)
}

func getGitUntrackedCount() int {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}
	return len(lines)
}

func getGitAheadBehind() (ahead, behind int) {
	cmd := exec.Command("git", "rev-list", "--count", "--left-right", "@{upstream}...HEAD")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0
	}

	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) == 2 {
		// Ignore scanf errors as these are non-critical for version display
		_, _ = fmt.Sscanf(parts[0], "%d", &behind)
		_, _ = fmt.Sscanf(parts[1], "%d", &ahead)
	}

	return ahead, behind
}
