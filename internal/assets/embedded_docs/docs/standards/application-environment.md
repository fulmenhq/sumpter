Sumpter Application Environment Standard

Single source of truth for home/work directory resolution, locations, and precedence.

---

## Goals

- Provide a predictable, cross-platform layout for configs, logs, cache, and temp/work artifacts.
- Support enterprise-friendly defaults with user overrides via flags and environment variables.

---

## Key Directories

- `SUMPTER_HOME`: root for user-level data and configuration
  - `configs/`: user configs, profiles (validated via JSON Schemas)
  - `logs/`: default target for log files when only a filename is provided
  - `cache/`: non-critical cached artifacts
  - `work/`: large transient artifacts and temp files

- `SUMPTER_WORKDIR`: preferred location for large temporary files; if not set, falls back to `SUMPTER_HOME/work`.

---

## Resolution Precedence

SUMPTER_HOME is determined in this order:

1. CLI flag: `--home <path>` (highest precedence)
2. Environment variable: `SUMPTER_HOME`
3. OS-specific user data directory:
   - macOS: `$HOME/Library/Application Support/Sumpter`
   - Linux: `$XDG_DATA_HOME/sumpter` or `$HOME/.local/share/sumpter`
   - Windows: `%AppData%\Sumpter`

SUMPTER_WORKDIR is determined in this order:

1. CLI flag: `--workdir <path>` (highest precedence)
2. Environment variable: `SUMPTER_WORKDIR`
3. `SUMPTER_HOME/work`
4. OS temp directory with per-run subfolder (e.g., `$TMPDIR/sumpter/<timestamp>`)

---

## Logging

- Default logger writes to stderr.
- `--log-file <path>` writes logs to the specified file (append-safe) in addition to stderr.
- When `--log-file` is provided without a directory component, logs are placed in `SUMPTER_HOME/logs/`.

---

## Permissions

- SUMPTER_HOME and subdirectories are created on demand with user-only permissions where possible.
- Workdir subfolders are cleaned up on normal exit when safe; long-running jobs may keep artifacts as needed.

---

## Environment Variables

- `SUMPTER_HOME`: overrides home directory path
- `SUMPTER_WORKDIR`: overrides work directory path

---

## Flags (when supported)

- `--home <path>`: override SUMPTER_HOME
- `--workdir <path>`: override SUMPTER_WORKDIR

---

## Validation (Future)

- A JSON Schema for environment profiles may be introduced under `schemas/env/` to validate persisted settings.
