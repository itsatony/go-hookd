# Git Hooks for go-hookd

This directory contains Git hooks that enforce vAudience.AI quality standards.

## Available Hooks

### pre-commit

Runs before every commit to ensure code quality:
- ✅ **Code Formatting**: Checks all files are formatted with `go fmt`
- ✅ **Static Analysis**: Runs `go vet` to catch common mistakes
- ✅ **Fast Tests**: Runs `go test -short -race` to verify tests pass

**Note**: Uses `-short` flag for speed; full test suite runs in CI.

## Installation

### Automatic Installation (Recommended)

Run the installation script from the project root:

```bash
./scripts/install-hooks.sh
```

This creates a symlink from `.git/hooks/pre-commit` to `.git-hooks/pre-commit`.

### Manual Installation

Create the symlink manually:

```bash
cd /home/itsatony/code/go-hookd
ln -sf ../../.git-hooks/pre-commit .git/hooks/pre-commit
```

### Verification

Test the hook works correctly:

```bash
# Should pass all checks
.git/hooks/pre-commit
```

## Usage

### Normal Commits

The hook runs automatically on every commit:

```bash
git commit -m "Your commit message"
# Hook runs automatically
# Commit proceeds if all checks pass
```

### Bypassing Hooks (Not Recommended)

In rare cases where you need to commit without running hooks:

```bash
git commit --no-verify -m "Emergency fix"
```

**Warning**: Only use `--no-verify` in genuine emergencies. All commits must pass quality gates before merging.

## What Each Check Does

### 1. Code Formatting (go fmt)

Ensures all Go code follows standard formatting:
- **Pass**: All files are properly formatted
- **Fail**: Lists files needing formatting
- **Fix**: Run `make fmt` or `go fmt ./...`

### 2. Static Analysis (go vet)

Catches common programming errors:
- Unreachable code
- Invalid struct tags
- Shadowed variables
- Invalid printf formats
- And more...

**Fix**: Address the issues reported by `go vet`

### 3. Fast Tests (go test -short -race)

Runs unit tests with race detector:
- **Pass**: All tests pass, no race conditions
- **Fail**: Test failures or race conditions detected
- **Fix**: Run `make test-short` to see full output

## Troubleshooting

### Hook Not Running

If the hook isn't executing:

1. Check it's executable:
   ```bash
   ls -l .git/hooks/pre-commit
   # Should show: -rwxr-xr-x
   ```

2. Re-run installation:
   ```bash
   ./scripts/install-hooks.sh
   ```

### Hook Too Slow

The pre-commit hook is optimized for speed:
- Uses `go test -short` (skips integration tests)
- Full test suite runs in CI/CD

If still too slow, consider:
- Running full tests manually: `make test`
- Using `git commit --no-verify` sparingly
- Optimizing test performance

### False Positives

If a check fails incorrectly:
1. Run the check manually to diagnose
2. Fix the underlying issue
3. Report if it's a genuine bug

## Integration with CI/CD

Pre-commit hooks provide **fast local feedback**. CI/CD provides **comprehensive validation**:

| Check | Pre-commit | CI/CD |
|-------|-----------|-------|
| Formatting | ✅ go fmt | ✅ go fmt |
| Static Analysis | ✅ go vet | ✅ go vet + golangci-lint |
| Tests | ✅ Short tests | ✅ Full test suite |
| Race Detection | ✅ Enabled | ✅ Enabled |
| Coverage | ❌ Skip | ✅ 90% threshold |

**Philosophy**: Pre-commit catches obvious issues quickly. CI catches everything.

## Customization

To modify hook behavior, edit `.git-hooks/pre-commit`:

```bash
# Example: Skip race detector for speed
go test -short ./...  # Remove -race flag

# Example: Add additional checks
go mod verify
golangci-lint run
```

After modifying, test thoroughly:

```bash
.git-hooks/pre-commit
```

## Hooks Philosophy

**vAudience.AI Standards**: "Excellence. Always."

Hooks enforce minimum quality standards:
- ✅ Prevent committing unformatted code
- ✅ Catch common programming errors
- ✅ Ensure tests pass before commit
- ✅ Enable race detection by default

**Benefits**:
- Faster feedback loop (seconds vs. minutes waiting for CI)
- Cleaner git history (fewer "fix formatting" commits)
- Higher code quality
- Better collaboration (consistent standards)

## References

- [Git Hooks Documentation](https://git-scm.com/book/en/v2/Customizing-Git-Git-Hooks)
- [vAudience.AI Code Rules](../docs/code_rules.md)
- [Makefile Targets](../Makefile)
