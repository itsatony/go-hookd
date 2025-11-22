#!/bin/bash
# install-hooks.sh
# Installs Git hooks for go-hookd quality enforcement

set -e

# Color codes
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo "🔧 Installing go-hookd Git hooks..."
echo ""

# Get script directory (works from anywhere)
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# Ensure we're in a git repository
if [ ! -d "$PROJECT_ROOT/.git" ]; then
    echo -e "${RED}❌ ERROR: Not a git repository${NC}"
    echo "   Run this script from the project root or scripts directory"
    exit 1
fi

# Ensure hooks directory exists
HOOKS_DIR="$PROJECT_ROOT/.git/hooks"
if [ ! -d "$HOOKS_DIR" ]; then
    echo -e "${YELLOW}⚠️  Creating .git/hooks directory...${NC}"
    mkdir -p "$HOOKS_DIR"
fi

# Install pre-commit hook
HOOK_SOURCE="$PROJECT_ROOT/.git-hooks/pre-commit"
HOOK_TARGET="$HOOKS_DIR/pre-commit"

if [ ! -f "$HOOK_SOURCE" ]; then
    echo -e "${RED}❌ ERROR: Hook source not found${NC}"
    echo "   Expected: $HOOK_SOURCE"
    exit 1
fi

# Check if hook is already installed
if [ -L "$HOOK_TARGET" ]; then
    # Symlink exists - check if it points to the right place
    CURRENT_TARGET=$(readlink "$HOOK_TARGET")
    if [ "$CURRENT_TARGET" = "../../.git-hooks/pre-commit" ]; then
        echo -e "${GREEN}✅ pre-commit hook already installed${NC}"
    else
        echo -e "${YELLOW}⚠️  Existing symlink points to: $CURRENT_TARGET${NC}"
        echo "   Updating to: ../../.git-hooks/pre-commit"
        rm "$HOOK_TARGET"
        ln -sf ../../.git-hooks/pre-commit "$HOOK_TARGET"
        echo -e "${GREEN}✅ pre-commit hook updated${NC}"
    fi
elif [ -f "$HOOK_TARGET" ]; then
    # Regular file exists - back it up
    echo -e "${YELLOW}⚠️  Existing pre-commit hook found${NC}"
    BACKUP="$HOOK_TARGET.backup.$(date +%Y%m%d_%H%M%S)"
    echo "   Backing up to: $(basename $BACKUP)"
    mv "$HOOK_TARGET" "$BACKUP"
    ln -sf ../../.git-hooks/pre-commit "$HOOK_TARGET"
    echo -e "${GREEN}✅ pre-commit hook installed (old hook backed up)${NC}"
else
    # No hook exists - install fresh
    ln -sf ../../.git-hooks/pre-commit "$HOOK_TARGET"
    echo -e "${GREEN}✅ pre-commit hook installed${NC}"
fi

# Ensure hook is executable
if [ ! -x "$HOOK_SOURCE" ]; then
    echo -e "${YELLOW}⚠️  Making hook executable...${NC}"
    chmod +x "$HOOK_SOURCE"
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "${GREEN}✅ Git hooks installed successfully!${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Installed hooks:"
echo "  • pre-commit - Enforces formatting, vet, and tests"
echo ""
echo "The hook will run automatically on every commit."
echo ""
echo "To test the hook manually:"
echo "  .git/hooks/pre-commit"
echo ""
echo "To bypass the hook (not recommended):"
echo "  git commit --no-verify"
echo ""
