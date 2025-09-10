#!/bin/bash

# Sumpter Coverage Threshold Validation Script
# Validates test coverage against lifecycle-aware thresholds
# Last Updated: September 3, 2025

set -e

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
COVERAGE_FILE="coverage/coverage.out"
CONFIG_FILE="config/coverage-thresholds.yaml"
LIFECYCLE_FILE="LIFECYCLE_PHASE"

# Function to print colored output
print_status() {
    local color=$1
    local message=$2
    echo -e "${color}${message}${NC}"
}

# Function to extract coverage percentage from go tool cover output
get_coverage() {
    local package=$1

    if [ "$package" = "total" ]; then
        local coverage_line=$(go tool cover -func="$COVERAGE_FILE" | grep "total:" | awk '{print $3}' | sed 's/%//')
        echo "${coverage_line:-0.0}"
        return
    fi

    # Calculate package coverage by averaging function coverages
    local package_functions=$(go tool cover -func="$COVERAGE_FILE" | grep "^github.com/fulmenhq/sumpter/$package" | awk '{print $3}' | sed 's/%//')

    if [ -z "$package_functions" ]; then
        echo "0.0"
        return
    fi

    # Calculate average of all function coverages in the package
    local total=0
    local count=0

    for coverage in $package_functions; do
        if [ "$coverage" != "0.0" ]; then
            total=$(echo "$total + $coverage" | bc -l 2>/dev/null || echo "$total")
            count=$((count + 1))
        fi
    done

    if [ $count -eq 0 ]; then
        echo "0.0"
    else
        echo "scale=1; $total / $count" | bc -l 2>/dev/null || echo "0.0"
    fi
}

# Function to get threshold from YAML config
get_threshold() {
    local phase=$1
    local package=$2

    if [ ! -f "$CONFIG_FILE" ]; then
        print_status $RED "❌ Coverage config file not found: $CONFIG_FILE"
        exit 1
    fi

    # Use yq if available, otherwise fall back to grep/sed
    if command -v yq >/dev/null 2>&1; then
        if [ "$package" = "total" ]; then
            yq eval ".phases.$phase.coverage" "$CONFIG_FILE" 2>/dev/null || echo "50"
        else
            # Try package-specific threshold first, then fall back to phase default
            local pkg_threshold=$(yq eval ".packages.\"github.com/fulmenhq/sumpter/$package\".$phase" "$CONFIG_FILE" 2>/dev/null)
            if [ "$pkg_threshold" != "null" ] && [ -n "$pkg_threshold" ]; then
                echo "$pkg_threshold"
            else
                yq eval ".phases.$phase.coverage" "$CONFIG_FILE" 2>/dev/null || echo "50"
            fi
        fi
    else
        # Fallback parsing with grep/sed
        if [ "$package" = "total" ]; then
            grep -A 5 "^phases:" "$CONFIG_FILE" | grep -A 2 "^  $phase:" | grep "coverage:" | awk '{print $2}' || echo "50"
        else
            # Try package-specific threshold first
            local pkg_threshold=$(grep -A 3 "^  github.com/fulmenhq/sumpter/$package:" "$CONFIG_FILE" | grep "$phase:" | awk '{print $2}' || echo "")
            if [ -n "$pkg_threshold" ]; then
                echo "$pkg_threshold"
            else
                # Fall back to phase default
                grep -A 5 "^phases:" "$CONFIG_FILE" | grep -A 2 "^  $phase:" | grep "coverage:" | awk '{print $2}' || echo "50"
            fi
        fi
    fi
}

# Main validation function
validate_coverage() {
    local phase=$1

    print_status $BLUE "🛡️ Sumpter - Coverage Validation for $phase Phase"
    echo

    # Check if coverage file exists
    if [ ! -f "$COVERAGE_FILE" ]; then
        print_status $RED "❌ Coverage file not found: $COVERAGE_FILE"
        print_status $YELLOW "   Run 'make test' to generate coverage data"
        exit 1
    fi

    # Get overall coverage
    local overall_coverage=$(get_coverage "total")
    local overall_threshold=$(get_threshold "$phase" "total")

    print_status $BLUE "Coverage Summary:"
    printf "  Overall: %.1f%% (threshold: %s%%)\n" "$overall_coverage" "$overall_threshold"

    # Check package-specific coverage
    local packages=("cmd/sumpter/commands" "internal/config" "internal/logging")

    local failed_packages=()
    local passed_packages=()

    for package in "${packages[@]}"; do
        local coverage=$(get_coverage "$package")
        local threshold=$(get_threshold "$phase" "$package")

        printf "  %s: %.1f%% (threshold: %s%%)\n" "$package" "$coverage" "$threshold"

        if (( $(echo "$coverage < $threshold" | bc -l 2>/dev/null || echo "1") )); then
            failed_packages+=("$package")
        else
            passed_packages+=("$package")
        fi
    done

    echo

    # Overall assessment
    if (( $(echo "$overall_coverage < $overall_threshold" | bc -l 2>/dev/null || echo "1") )); then
        print_status $RED "❌ Overall coverage FAILED: $overall_coverage% < $overall_threshold%"
        overall_failed=true
    else
        print_status $GREEN "✅ Overall coverage PASSED: $overall_coverage% >= $overall_threshold%"
        overall_failed=false
    fi

    # Package assessment
    if [ ${#failed_packages[@]} -eq 0 ]; then
        print_status $GREEN "✅ All package coverage requirements met"
    else
        print_status $RED "❌ Package coverage FAILED for: ${failed_packages[*]}"
        for package in "${failed_packages[@]}"; do
            local coverage=$(get_coverage "github.com/fulmenhq/sumpter/$package")
            local threshold=$(get_threshold "$phase" "$package")
            print_status $YELLOW "   $package: $coverage% < ${threshold}% required"
        done
    fi

    # Final result
    echo
    if [ "$overall_failed" = true ] || [ ${#failed_packages[@]} -gt 0 ]; then
        print_status $RED "❌ Coverage validation FAILED"
        print_status $YELLOW "💡 To improve coverage:"
        print_status $YELLOW "   1. Add more unit tests to failing packages"
        print_status $YELLOW "   2. Focus on core functionality first"
        print_status $YELLOW "   3. Run 'make test-coverage' for detailed report"
        exit 1
    else
        print_status $GREEN "✅ Coverage validation PASSED"
        print_status $GREEN "🎉 Ready for $phase phase requirements!"
    fi
}

# Main script
main() {
    # Get current lifecycle phase
    if [ ! -f "$LIFECYCLE_FILE" ]; then
        print_status $RED "❌ Lifecycle phase file not found: $LIFECYCLE_FILE"
        exit 1
    fi

    local phase=$(cat "$LIFECYCLE_FILE" | tr -d '\n')

    if [ -z "$phase" ]; then
        print_status $RED "❌ No lifecycle phase found in $LIFECYCLE_FILE"
        exit 1
    fi

    # Allow override via command line argument
    if [ $# -gt 0 ]; then
        phase=$1
        print_status $YELLOW "⚠️ Using override phase: $phase"
    fi

    validate_coverage "$phase"
}

# Run main function
main "$@"
