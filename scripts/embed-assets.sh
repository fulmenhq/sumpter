#!/bin/bash
# Embed assets script for Sumpter
# Syncs SSOT (Single Source of Truth) to embedded mirrors

set -e

echo "🔨 Embedding Sumpter assets..."

# Create embedded directories if they don't exist
mkdir -p internal/assets/embedded_docs/docs
mkdir -p internal/assets/embedded_schemas/schemas
mkdir -p internal/assets/embedded_examples/examples

# Sync docs (using goneat content command if available)
if command -v goneat &> /dev/null; then
    echo "📚 Syncing documentation using goneat content command..."
    goneat content embed --manifest docs/embed-manifest.yaml --root docs --target internal/assets/embedded_docs/docs
else
    echo "📚 Syncing documentation using rsync..."
    rsync -av --delete docs/ internal/assets/embedded_docs/docs/
fi

# Sync schemas
echo "📋 Syncing schemas..."
rsync -av --delete schemas/ internal/assets/embedded_schemas/schemas/

# Sync examples
echo "📝 Syncing examples..."
rsync -av --delete examples/ internal/assets/embedded_examples/examples/

echo "✅ Asset embedding complete!"
echo "📦 Embedded content:"
echo "  - docs/ → internal/assets/embedded_docs/docs/"
echo "  - schemas/ → internal/assets/embedded_schemas/schemas/"
echo "  - examples/ → internal/assets/embedded_examples/examples/"
