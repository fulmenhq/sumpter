# Version Command

Display version information and build details for Sumpter.

## Usage

```bash
sumpter version [flags]
```

## Description

The `version` command displays comprehensive version information about your Sumpter installation, including build details, Git commit information, and system metadata.

## Flags

- `--extended`: Show detailed build and Git information
- `--json`: Output version information in JSON format

## Examples

### Basic Version Information

```bash
sumpter version
```

Output:

```
Sumpter v0.1.0
Go Version: go1.21.0
OS/Arch: linux/amd64
```

### Extended Version Information

```bash
sumpter version --extended
```

Output:

```
Sumpter v0.1.0
Git commit: a1b2c3d (dirty-+1 ~2 ?3)
Git branch: main (ahead 2, behind 1)
Build time: 2024-01-15_14:30:25
Platform: linux/amd64
Go version: go1.21.0
Environment: development
```

### JSON Output

```bash
sumpter version --json
```

Output:

```json
{
  "version": "0.1.0",
  "build_time": "2024-01-15_14:30:25",
  "git_commit": "a1b2c3d4e5f6g7h8i9j0",
  "go_version": "go1.21.0",
  "platform": "linux",
  "arch": "amd64",
  "git_status": {
    "branch": "main",
    "commit": "a1b2c3d",
    "clean": false,
    "staged": 1,
    "unstaged": 2,
    "untracked": 3,
    "ahead": 2,
    "behind": 1
  },
  "environment": "development"
}
```

## Information Displayed

### Basic Information

- **Version**: Current Sumpter version from VERSION file
- **Go Version**: Go runtime version used to build Sumpter
- **OS/Arch**: Operating system and architecture

### Extended Information (with --extended)

- **Git Commit**: Current Git commit hash (short form)
- **Git Branch**: Current branch name with ahead/behind status
- **Build Time**: When the binary was built (UTC)
- **Platform**: Operating system details
- **Go Version**: Full Go version string
- **Environment**: Detected environment (development/production)

### Git Status Indicators

When Git information is available, the extended output shows:

- **Clean/Dirty**: Whether working directory has uncommitted changes
- **Staged**: Number of staged changes (+N)
- **Unstaged**: Number of unstaged changes (~N)
- **Untracked**: Number of untracked files (?N)
- **Ahead/Behind**: Commits ahead/behind remote branch

## Use Cases

- **Version Verification**: Confirm which version of Sumpter is installed
- **Build Information**: Get build timestamp and Git commit for debugging
- **Environment Detection**: Understand the deployment environment
- **CI/CD Integration**: Use JSON output for automated version checking
- **Support Requests**: Provide version details when reporting issues

## Notes

- Version information is injected at build time using the VERSION file
- Git information is only available if Sumpter was built from a Git repository
- The `--json` flag provides machine-readable output for scripts and automation
- Environment detection uses common environment variables (ENV, NODE_ENV, etc.)
