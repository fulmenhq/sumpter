#!/bin/bash
# Verify embedded assets script for Sumpter
# Ensures embedded mirrors match SSOT

set -e

echo "🔍 Verifying embedded assets..."

# Check if goneat is available for content verification
if command -v goneat &>/dev/null; then
	echo "📚 Verifying documentation using goneat content command..."
	if ! goneat content verify --manifest docs/embed-manifest.yaml --root docs --target internal/assets/embedded_docs/docs; then
		echo "❌ Documentation verification failed!"
		echo "💡 Run 'make embed-assets' to sync documentation"
		exit 1
	fi
else
	echo "📚 Verifying documentation using rsync dry-run..."
	if ! rsync -av --delete --dry-run docs/ internal/assets/embedded_docs/docs/ | grep -q "deleting\|sending"; then
		echo "❌ Documentation verification failed - mirrors are out of sync!"
		echo "💡 Run 'make embed-assets' to sync documentation"
		exit 1
	fi
fi

# Verify schemas
echo "📋 Verifying schemas..."
if ! rsync -av --delete --dry-run schemas/ internal/assets/embedded_schemas/schemas/ | grep -q "deleting\|sending"; then
	echo "❌ Schema verification failed - mirrors are out of sync!"
	echo "💡 Run 'make embed-assets' to sync schemas"
	exit 1
fi

# Verify examples
echo "📝 Verifying examples..."
if ! rsync -av --delete --dry-run examples/ internal/assets/embedded_examples/examples/ | grep -q "deleting\|sending"; then
	echo "❌ Examples verification failed - mirrors are out of sync!"
	echo "💡 Run 'make embed-assets' to sync examples"
	exit 1
fi

echo "✅ All embedded assets verified successfully!"
