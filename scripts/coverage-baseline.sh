#!/bin/bash
# coverage-baseline.sh
# Track test coverage baseline for go-hookd
# Helps achieve and maintain 90% coverage goal

set -e

# Color codes
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
COVERAGE_THRESHOLD=90
COVERAGE_FILE="coverage.out"
BASELINE_FILE=".coverage-baseline.json"
REPORT_FILE="coverage-report.txt"

# Get script directory
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"
cd "$PROJECT_ROOT"

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}                     go-hookd Coverage Baseline Tracking${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Function to generate coverage report
generate_coverage() {
    echo -e "${CYAN}📊 Generating coverage report...${NC}"
    go test -cover -coverprofile="$COVERAGE_FILE" ./... > /dev/null 2>&1

    if [ ! -f "$COVERAGE_FILE" ]; then
        echo -e "${RED}❌ Failed to generate coverage report${NC}"
        exit 1
    fi

    echo -e "${GREEN}✅ Coverage report generated${NC}"
    echo ""
}

# Function to extract total coverage percentage
get_total_coverage() {
    go tool cover -func="$COVERAGE_FILE" | grep total | awk '{print $3}' | sed 's/%//'
}

# Function to get per-package coverage
get_package_coverage() {
    go tool cover -func="$COVERAGE_FILE" | grep -v "^total:" | awk '{
        # Extract package name from file path
        split($1, parts, "/")
        pkg = parts[1]
        for(i=2; i<length(parts); i++) {
            pkg = pkg "/" parts[i]
        }

        # Accumulate coverage by package
        gsub(/%/, "", $3)
        pkg_coverage[pkg] += $3
        pkg_count[pkg]++
    }
    END {
        for(pkg in pkg_coverage) {
            avg = pkg_coverage[pkg] / pkg_count[pkg]
            printf "%s %.1f\n", pkg, avg
        }
    }' | sort -t' ' -k2 -n
}

# Function to identify low coverage files
get_low_coverage_files() {
    local threshold=$1
    go tool cover -func="$COVERAGE_FILE" | grep -v "^total:" | awk -v thresh="$threshold" '{
        gsub(/%/, "", $3)
        if($3 < thresh) {
            printf "%s %.1f%%\n", $1, $3
        }
    }' | sort -t' ' -k2 -n
}

# Function to save baseline
save_baseline() {
    local total_coverage=$1
    local timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

    # Create or update baseline file
    if [ ! -f "$BASELINE_FILE" ]; then
        echo "{\"history\": []}" > "$BASELINE_FILE"
    fi

    # Append current coverage to history
    local temp_file=$(mktemp)
    jq ".history += [{\"timestamp\": \"$timestamp\", \"coverage\": $total_coverage}]" "$BASELINE_FILE" > "$temp_file"
    mv "$temp_file" "$BASELINE_FILE"

    echo -e "${GREEN}✅ Baseline saved${NC}"
}

# Function to show coverage trend
show_trend() {
    if [ ! -f "$BASELINE_FILE" ]; then
        echo -e "${YELLOW}⚠️  No baseline history found${NC}"
        return
    fi

    echo -e "${CYAN}📈 Coverage Trend (last 10 measurements):${NC}"
    echo ""

    jq -r '.history[-10:] | .[] | "\(.timestamp) - \(.coverage)%"' "$BASELINE_FILE" | while read line; do
        echo "   $line"
    done

    echo ""

    # Calculate trend (increasing/decreasing)
    local first=$(jq -r '.history[-10] | .coverage // 0' "$BASELINE_FILE")
    local last=$(jq -r '.history[-1] | .coverage // 0' "$BASELINE_FILE")

    if [ "$first" != "0" ] && [ "$last" != "0" ]; then
        local diff=$(echo "$last - $first" | bc)
        if [ "$(echo "$diff > 0" | bc)" -eq 1 ]; then
            echo -e "${GREEN}📈 Trend: INCREASING (+${diff}% over last 10 measurements)${NC}"
        elif [ "$(echo "$diff < 0" | bc)" -eq 1 ]; then
            echo -e "${RED}📉 Trend: DECREASING (${diff}% over last 10 measurements)${NC}"
        else
            echo -e "${YELLOW}➡️  Trend: STABLE (no change)${NC}"
        fi
    fi

    echo ""
}

# Function to show roadmap to 90%
show_roadmap() {
    local current_coverage=$1
    local target=$COVERAGE_THRESHOLD

    echo -e "${CYAN}🎯 Roadmap to ${target}% Coverage:${NC}"
    echo ""

    if [ "$(echo "$current_coverage >= $target" | bc)" -eq 1 ]; then
        echo -e "${GREEN}✅ TARGET ACHIEVED! Current coverage: ${current_coverage}%${NC}"
        return
    fi

    local gap=$(echo "$target - $current_coverage" | bc)
    echo -e "   Current: ${current_coverage}%"
    echo -e "   Target:  ${target}%"
    echo -e "   Gap:     ${RED}${gap}%${NC}"
    echo ""

    echo -e "${CYAN}📋 Priority Actions:${NC}"
    echo ""

    # Show files with lowest coverage
    echo "   Files with lowest coverage (<50%):"
    get_low_coverage_files 50 | head -20 | while read file coverage; do
        echo -e "      ${RED}${coverage}${NC} - $file"
    done

    echo ""
    echo -e "${CYAN}💡 Recommendations:${NC}"
    echo "   1. Focus on files with <50% coverage first (biggest impact)"
    echo "   2. Add error path testing (most common gap)"
    echo "   3. Test concurrent scenarios (race conditions)"
    echo "   4. Test edge cases (nil, empty, invalid inputs)"
    echo "   5. Run 'make coverage' to see detailed HTML report"
    echo ""
}

# Function to generate detailed report
generate_detailed_report() {
    local total_coverage=$1

    echo -e "${CYAN}📝 Generating detailed report...${NC}"

    cat > "$REPORT_FILE" <<EOF
go-hookd Test Coverage Report
Generated: $(date)
================================================================================

SUMMARY
-------
Total Coverage:    ${total_coverage}%
Coverage Target:   ${COVERAGE_THRESHOLD}%
Status:            $([ "$(echo "$total_coverage >= $COVERAGE_THRESHOLD" | bc)" -eq 1 ] && echo "✅ PASSING" || echo "❌ FAILING")

PACKAGE COVERAGE
---------------
EOF

    get_package_coverage | while read pkg coverage; do
        printf "%-50s %6.1f%%\n" "$pkg" "$coverage" >> "$REPORT_FILE"
    done

    cat >> "$REPORT_FILE" <<EOF

LOW COVERAGE FILES (<70%)
-------------------------
EOF

    get_low_coverage_files 70 | while read file coverage; do
        printf "%-80s %6s\n" "$file" "$coverage" >> "$REPORT_FILE"
    done

    echo "" >> "$REPORT_FILE"
    echo "For detailed HTML report, run: go tool cover -html=$COVERAGE_FILE" >> "$REPORT_FILE"

    echo -e "${GREEN}✅ Detailed report saved to: $REPORT_FILE${NC}"
    echo ""
}

# Main execution
main() {
    # Generate coverage
    generate_coverage

    # Extract total coverage
    TOTAL_COVERAGE=$(get_total_coverage)

    if [ -z "$TOTAL_COVERAGE" ]; then
        echo -e "${RED}❌ Failed to extract coverage percentage${NC}"
        exit 1
    fi

    # Display current coverage
    echo -e "${CYAN}📊 Current Coverage: ${YELLOW}${TOTAL_COVERAGE}%${NC}"
    echo ""

    # Check against threshold
    if [ "$(echo "$TOTAL_COVERAGE >= $COVERAGE_THRESHOLD" | bc)" -eq 1 ]; then
        echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
        echo -e "${GREEN}✅ COVERAGE TARGET ACHIEVED: ${TOTAL_COVERAGE}% >= ${COVERAGE_THRESHOLD}%${NC}"
        echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    else
        echo -e "${RED}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
        echo -e "${RED}❌ COVERAGE BELOW TARGET: ${TOTAL_COVERAGE}% < ${COVERAGE_THRESHOLD}%${NC}"
        echo -e "${RED}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    fi
    echo ""

    # Save baseline
    save_baseline "$TOTAL_COVERAGE"
    echo ""

    # Show trend
    show_trend

    # Show roadmap
    show_roadmap "$TOTAL_COVERAGE"

    # Generate detailed report
    generate_detailed_report "$TOTAL_COVERAGE"

    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}                              Coverage Analysis Complete${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo "Next steps:"
    echo "  • View detailed report: cat $REPORT_FILE"
    echo "  • View HTML coverage:   go tool cover -html=$COVERAGE_FILE"
    echo "  • View baseline trend:  cat $BASELINE_FILE"
    echo ""
}

# Run main function
main
