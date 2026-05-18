#!/bin/bash
# Embed assets script for Sumpter
# Syncs SSOT (Single Source of Truth) to embedded mirrors

set -e

echo "🔨 Embedding Sumpter assets..."

# Create embedded directories if they don't exist
mkdir -p internal/assets/embedded_docs/docs
mkdir -p internal/assets/embedded_schemas/schemas
mkdir -p internal/assets/embedded_examples/examples
mkdir -p internal/assets/embedded_templates/templates

# Sync docs (using goneat content command if available)
if command -v goneat &>/dev/null; then
	echo "📚 Syncing documentation using goneat content command..."
	goneat content embed --manifest docs/embed-manifest.yaml --root docs --target internal/assets/embedded_docs/docs
else
	echo "📚 Syncing documentation using rsync..."
	rsync -av --delete docs/ internal/assets/embedded_docs/docs/
fi

# Sync schemas
echo "📋 Syncing schemas..."
if command -v rsync &>/dev/null; then
	rsync -av --delete schemas/ internal/assets/embedded_schemas/schemas/
else
	echo "📋 Syncing schemas using Go script..."
	go run scripts/sync-assets.go schemas internal/assets/embedded_schemas/schemas
fi

# Sync examples
echo "📝 Syncing examples..."
if command -v rsync &>/dev/null; then
	rsync -av --delete --delete-excluded --exclude '.scratchpad/' --exclude '*/.scratchpad/' examples/ internal/assets/embedded_examples/examples/
else
	echo "📝 Syncing examples using Go script..."
	go run scripts/sync-assets.go examples internal/assets/embedded_examples/examples
fi

# Sync templates
echo "🧩 Syncing templates..."
rsync -av --delete templates/ internal/assets/embedded_templates/templates/

echo "✅ Asset embedding complete!"
echo "📦 Embedded content:"
echo "  - docs/ → internal/assets/embedded_docs/docs/"
echo "  - schemas/ → internal/assets/embedded_schemas/schemas/"
echo "  - examples/ → internal/assets/embedded_examples/examples/"
echo "  - templates/ → internal/assets/embedded_templates/templates/"
