#!/usr/bin/env bash
# Verify that embedded mirrors under internal/assets/embedded_* match the SSOT
# trees (docs/, schemas/, examples/, templates/).
#
# Strategy: run scripts/embed-assets.sh, then check whether `git diff` reports
# any change under the embedded paths. If yes, drift exists — the SSOT was
# updated without re-syncing the mirror (or vice versa). The working tree is
# restored before exit so this script is a no-op on success and on detected
# drift alike.
#
# Exit codes:
#   0  — embedded mirrors match SSOT
#   1  — drift detected; fix by running `make embed-assets` and committing
#   2  — preflight refused; embedded paths already had uncommitted changes
#
# Usage:
#   ./scripts/verify-embeds.sh
#   make verify-embeds

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

EMBED_DIRS=(
  internal/assets/embedded_docs
  internal/assets/embedded_schemas
  internal/assets/embedded_examples
  internal/assets/embedded_templates
)

echo "🔍 Verifying embedded assets are in sync with SSOT..."

# Preflight: refuse if embedded paths are already dirty.
# We cannot reliably detect drift if the user has uncommitted edits in
# embedded_* — the post-sync diff would confuse those edits with sync drift.
if ! git diff --quiet -- "${EMBED_DIRS[@]}" 2>/dev/null \
   || ! git diff --cached --quiet -- "${EMBED_DIRS[@]}" 2>/dev/null; then
  echo "ℹ️  Embedded mirror paths have uncommitted changes."
  echo "    verify-embeds.sh refuses to run because it cannot distinguish your"
  echo "    in-progress edits from drift introduced by an unsynced SSOT."
  echo ""
  echo "    Either commit/stash the embedded changes first, or run"
  echo "    \`scripts/embed-assets.sh\` directly and inspect the result manually."
  echo ""
  echo "    Affected paths:"
  git diff --name-only HEAD -- "${EMBED_DIRS[@]}" | sed 's/^/      /'
  exit 2
fi

echo "🔄 Running scripts/embed-assets.sh to regenerate mirrors from SSOT..."
./scripts/embed-assets.sh > /dev/null 2>&1

if git diff --quiet -- "${EMBED_DIRS[@]}"; then
  echo "✅ Embedded mirrors match SSOT."
  exit 0
fi

echo "❌ Drift detected — embedded mirrors differ from SSOT after running"
echo "   scripts/embed-assets.sh. The committed mirror is stale relative to"
echo "   the source it claims to mirror."
echo ""
echo "Files that would change on re-sync:"
git diff --stat -- "${EMBED_DIRS[@]}" | sed 's/^/  /'
echo ""
echo "💡 To fix:"
echo "   1) Run \`make embed-assets\` (or \`scripts/embed-assets.sh\`)"
echo "   2) Commit the embedded mirror changes alongside the SSOT changes that"
echo "      caused the drift."
echo ""

# Restore the working tree so this check is a no-op for the caller's working
# state. The drift remains in main; only `make embed-assets` + commit fixes it.
echo "🧹 Restoring working tree (mirrors back to committed state)..."
git checkout -- "${EMBED_DIRS[@]}"

exit 1
