# ADR 0003: Unified Command Syntax Patterns

Status: Accepted
Date: 2025-09-24

## Context

Sumpter's CLI is growing with multiple commands (inspect, envinfo, validate, and upcoming extract/retrieve). Without standardized syntax patterns, users face inconsistent interfaces and developers create ad-hoc flag designs. This leads to poor usability, maintenance burden, and violated user expectations.

The initial extract prototype used `--dir` for input paths, creating inconsistency with other commands. A unified syntax framework is needed to ensure consistent, predictable CLI behavior across all Sumpter commands.

## Decision

### Core Syntax Categories

All Sumpter commands will follow these standardized flag categories and naming conventions:

#### 1. Input/Output Path Flags

```
--input-path PATH        # Primary input location (files/directories)
--output-path PATH       # Primary output destination
--config-path PATH       # Configuration file location
--log-file PATH          # Log output file (inherited from root)
```

#### 2. Content Control Flags

```
--include-pattern GLOB   # File/directory inclusion patterns
--exclude-pattern GLOB   # File/directory exclusion patterns
--max-depth INT         # Directory traversal depth limit
--follow-symlinks       # Follow symbolic links (boolean)
```

#### 3. Processing Control Flags

```
--batch-size INT        # Processing batch size
--workers INT          # Number of parallel workers
--dry-run              # Preview operation without execution
--force                # Overwrite existing files/destructive operations
```

#### 4. Output Format Flags

```
--format/-f FORMAT     # Output format: json, yaml, table, markdown, etc.
--output/-o PATH       # Output file path (stdout if not specified)
--json/-j              # Shortcut for --format json (boolean)
--pretty               # Pretty-print output (boolean)
```

#### 5. Filtering & Selection Flags

```
--filter EXPR          # Content filtering expression
--limit INT            # Result count limit
--sort-by FIELD        # Sort field name
--reverse              # Reverse sort order (boolean)
```

#### 6. Progress & Logging Flags

```
--progress/-p          # Show progress indicators (boolean)
--verbose/-v           # Verbose output mode (boolean)
--quiet/-q             # Minimal/silent output mode (boolean)
--debug                # Debug output mode (boolean)
```

### Flag Naming Conventions

1. **Hyphen-separated**: Multi-word flags use hyphens (`input-path`, `output-path`)
2. **Consistent abbreviations**: Short flags use single letters where logical (`-f`, `-o`, `-p`, `-v`)
3. **Boolean flags**: Simple enable/disable flags without values
4. **Path flags**: End with `-path` for clarity (`input-path`, `config-path`)
5. **Pattern flags**: Use `-pattern` for glob/file matching (`include-pattern`)

### Command-Specific Patterns

#### Data Processing Commands (inspect, extract, retrieve)

```bash
# File discovery and processing
--input-path /data/source
--include-pattern "*.xml"
--exclude-pattern "*/temp/*"
--max-depth 3
--follow-symlinks

# Output control
--output-path /data/output
--format json
--progress

# Processing control
--workers 4
--batch-size 100
```

#### Configuration Commands (validate, envinfo)

```bash
# Input specification
--input-path /path/to/configs
--config-path /path/to/sumpter.yaml

# Output control
--format table
--json
--output results.json
```

#### Root/Global Flags (inherited by all commands)

```bash
# Environment
--home /custom/sumpter/home
--workdir /tmp/sumpter
--config /path/to/config.yaml

# Logging
--log-level info
--log-format console
--log-file /var/log/sumpter.log
--log-color
--log-telemetry
```

## Consequences

### Positive

- **Consistent UX**: Users learn one syntax pattern applicable to all commands
- **Predictable behavior**: Flag meanings are standardized across commands
- **Easier maintenance**: Developers follow established patterns
- **Better discoverability**: `--help` output follows familiar conventions
- **Future-proofing**: New commands automatically fit the established framework

### Negative

- **Breaking changes**: Existing commands may need flag renaming (e.g., `--dir` → `--input-path`)
- **Pattern rigidity**: Some commands may need exceptions for domain-specific flags
- **Adoption overhead**: All future development must follow these patterns

### Migration Strategy

1. **Immediate**: New commands (extract, retrieve) use unified patterns from inception
2. **Gradual**: Existing commands migrate flags in backward-compatible way
3. **Deprecation**: Old flags show warnings and remain functional during transition
4. **Removal**: Legacy flags removed in next major version

## Alternatives Considered

### Minimal Standardization

- Only standardize flag naming without categories
- **Rejected**: Doesn't provide enough structure for complex commands

### Command-Specific Autonomy

- Each command defines its own syntax
- **Rejected**: Leads to inconsistent UX and maintenance burden

### Strict POSIX Compliance

- Follow GNU/POSIX flag conventions exactly
- **Rejected**: Too restrictive for modern CLI patterns, conflicts with existing flags

### Configuration-Driven Syntax

- Define command syntax in YAML configs
- **Rejected**: Adds complexity without clear benefits for this use case

## Implementation

### Validation Checklist

QA team will evaluate new commands against this ADR:

- [ ] All flags follow naming conventions
- [ ] Flags are categorized appropriately
- [ ] Short flags (-x) are logical and available
- [ ] Boolean flags don't require values
- [ ] Path flags end with `-path`
- [ ] No conflicting abbreviations
- [ ] Help text follows consistent formatting

### Code Standards

```go
// Example implementation pattern
type CommandOptions struct {
    InputPath       string
    OutputPath      string
    IncludePattern  string
    Workers         int
    Format          string
    Progress        bool
    DryRun          bool
}

func addStandardFlags(cmd *cobra.Command, opts *CommandOptions) {
    cmd.Flags().StringVar(&opts.InputPath, "input-path", "", "Input path for processing")
    cmd.Flags().StringVar(&opts.OutputPath, "output-path", "", "Output destination path")
    cmd.Flags().StringVar(&opts.IncludePattern, "include-pattern", "*", "File inclusion pattern")
    cmd.Flags().IntVar(&opts.Workers, "workers", 1, "Number of parallel workers")
    cmd.Flags().StringVarP(&opts.Format, "format", "f", "table", "Output format")
    cmd.Flags().BoolVarP(&opts.Progress, "progress", "p", false, "Show progress indicators")
    cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Preview operation without execution")
}
```

## References

- Current command implementations in `cmd/sumpter/commands/`
- ADR 0001: Schema-First Programmatic Outputs
- ADR 0002: Logging to stderr, JSON for telemetry, pretty console
- SOP: `docs/sop/repository-operations-sop.md`
