#!/bin/bash
# License compliance check script for Sumpter
# Ensures LICENSE file exists and dependencies are compatible

set -e

echo "🔍 Checking license compliance..."

# Check for LICENSE file
if [ ! -f "LICENSE" ]; then
	echo "❌ ERROR: LICENSE file not found"
	exit 1
fi

echo "✅ LICENSE file present"

# Check for NOTICE file
if [ ! -f "NOTICE" ]; then
	echo "❌ ERROR: NOTICE file not found"
	exit 1
fi

echo "✅ NOTICE file present"

# Check LICENSE content (should be Apache 2.0)
if ! grep -q "Apache License" LICENSE; then
	echo "❌ ERROR: LICENSE does not appear to be Apache 2.0"
	exit 1
fi

echo "✅ LICENSE appears to be Apache 2.0"

# Check go.mod for problematic dependencies
# This is a basic check - in production, use go-licenses or similar
echo "🔍 Checking go.mod dependencies..."

if grep -q "github.com/fulmenhq/goneat" go.mod; then
	echo "✅ Internal dependency: fulmenhq/goneat (assumed Apache 2.0)"
else
	echo "⚠️  WARNING: fulmenhq/goneat not found in dependencies"
fi

# Check for common problematic licenses (basic grep)
PROBLEMATIC_LICENSES=("GPL" "LGPL" "CDDL" "MPL" "EPL")

for license in "${PROBLEMATIC_LICENSES[@]}"; do
	if grep -r -i "$license" go.mod >/dev/null 2>&1; then
		echo "⚠️  WARNING: Potential $license license found in dependencies"
		echo "   Manual review recommended"
	fi
done

echo "✅ Basic dependency license check completed"

# Optional: Check for license headers in Go files
echo "🔍 Checking for license headers in Go files..."

GO_FILES=$(find . -name "*.go" -not -path "./vendor/*" | head -10)
MISSING_HEADERS=0

for file in $GO_FILES; do
	if ! head -5 "$file" | grep -q "Copyright\|Licensed under"; then
		echo "⚠️  WARNING: No license header in $file"
		MISSING_HEADERS=$((MISSING_HEADERS + 1))
	fi
done

if [ $MISSING_HEADERS -gt 0 ]; then
	echo "⚠️  WARNING: $MISSING_HEADERS Go files missing license headers"
	echo "   This is allowed under Apache 2.0 but headers are recommended"
else
	echo "✅ License headers found in sampled Go files"
fi

echo ""
echo "🎉 License compliance check completed successfully!"
echo ""
echo "Note: For comprehensive license checking, consider using:"
echo "  - go-licenses (https://github.com/google/go-licenses)"
echo "  - licensee (https://github.com/licensee/licensee)"
echo "  - fossa (https://github.com/fossa-contrib/fossa-cli)"
