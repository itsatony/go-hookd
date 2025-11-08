# CodeFinder MCP Agent Reference (v3.0/v0.10.0+)

**Purpose**: Enable LLM agents to efficiently use CodeFinder's 5 core MCP tools for code analysis.
**Philosophy**: Token-efficient operations. Always start minimal, expand progressively.

---

## Supported Languages (9 Total)

**Application**: Python (.py), JavaScript (.js), TypeScript (.ts/.tsx), Go (.go)
**DevOps**: Bash (.sh/.bash/.zsh), Makefile, Dockerfile, Docker Compose (.yml)
**Docs**: Markdown (.md)

All use tree-sitter parsing with full symbol indexing (functions, classes, methods, variables, imports, targets, services, etc.).

---

## 5 Core Tools - Quick Matrix

| Tool | Use When | Token Cost | Time |
|------|----------|-----------|------|
| `query()` | Find symbols, search patterns | 30-80/result | <100ms |
| `traverse()` | Dependencies, class hierarchies | 200-500 | 100-300ms |
| `batch_files()` | Multi-file operations | 50-200/file | 200-500ms |
| `aggregate()` | Metrics, code quality, counts | 300-800 | 150-400ms |
| `diff()` | Change impact, what-changed analysis | 400-1000 | 200-600ms |

---

## Tool 1: query() - Universal Search

**Signature**:
```python
query(
    q: str | list[str],        # Query string(s). Use "*" for all in scope
    t: str | list[str] = None, # Type: "fn","cls","mth","var","imp","tgt","svc"
    f: str | list[str] = None, # File path(s) - exact match
    d: str | list[str] = None, # Directory path(s)
    g: str = None,             # Glob: "**/*.py", "src/**/*.go"
    m: str = "x",              # Mode: "x"=exact, "f"=fuzzy, "r"=regex
    ctx: str = "std",          # Context: "min"(10t),"std"(30t),"full"(80t)
    rel: bool = False,         # Include relationships (adds ~40t/result)
    src: int = 0,              # Source lines (0=none, N=context lines)
    lim: int = 50,             # Max results
    fmt: str = "auto"          # Format: "auto","c"(compact),"v"(verbose)
)
```

**Critical Rules**:
- **ALWAYS** start with `ctx="min"` for searches returning >5 results
- Use `m="x"` (exact) when you know the name - it's 2-3x faster than fuzzy
- Apply filters (`t`, `f`, `d`, `g`) at query time, never filter results manually
- Set `rel=True` only when you need parent/child relationships immediately
- Use batch queries `q=["name1","name2","name3"]` for multiple exact matches

**Common Patterns**:

```python
# Find specific function (exact, full context)
query(q="authenticate_user", t="fn", m="x", ctx="full")  # 80t

# Explore related functions (fuzzy, minimal first)
query(q="auth", t="fn", m="f", ctx="min", lim=20)  # 120t
# Then get details: query(q="authenticate_user", m="x", ctx="full")

# All classes in file
query(q="*", t="cls", f="src/models/user.py", ctx="std")  # ~200t

# Pattern search (regex)
query(q="^get_.*_by_id$", t="fn", m="r", ctx="min")  # ~180t

# Batch exact matches
query(q=["UserService","AccountService","AuthService"], t="cls", m="x", ctx="std")  # ~240t

# Type filter across codebase
query(q="Service", t="cls", m="f", ctx="min", lim=30)  # Find all *Service classes

# Glob pattern (Python API files only)
query(q="*", t="fn", g="src/api/**/*.py", ctx="min", lim=50)  # ~350t
```

**Response Structure** (compact):
```json
{
  "r": [
    {"id":"a1b2","n":"authenticate_user","t":"fn","f":"src/api/auth.py","l":45,"sig":"def authenticate_user(...)","doc":"..."}
  ],
  "m": {"t":1,"q":1,"c":"80ms"}
}
```
- Extract `id` for chaining with `traverse()`
- `t` = total results, `q` = queries run, `c` = compute time

---

## Tool 2: traverse() - Graph Traversal

**Signature**:
```python
traverse(
    start: list[str],          # Symbol IDs or file paths
    typ: str,                  # Type: "dep"=dependencies, "hier"=hierarchy
    dir: str = "down",         # Direction: "down","up","both"
    depth: int = 2,            # Traversal depth (1-5 recommended)
    ctx: str = "std",          # Context per node
    fmt: str = "tree",         # Format: "tree","flat","graph"
    filters: dict = None       # {"types":["fn","cls"],"files":["*.py"]}
)
```

**Critical Rules**:
- Use symbol IDs from `query()` results, not names - 30% faster
- `depth=2` is optimal for most cases (3+ gets exponentially larger)
- `dir="down"` = what this uses, `dir="up"` = what uses this
- `typ="dep"` for function calls, `typ="hier"` for class inheritance
- Always set `filters` to reduce noise

**Common Patterns**:

```python
# What does this function call? (dependencies downstream)
traverse(start=["sym_id"], typ="dep", dir="down", depth=2, ctx="min")  # 200t

# What calls this function? (reverse dependencies)
traverse(start=["sym_id"], typ="dep", dir="up", depth=3, ctx="min")  # 350t

# Full dependency tree (bidirectional)
traverse(start=["sym_id"], typ="dep", dir="both", depth=2, ctx="std")  # 450t

# Class hierarchy (inheritance chain)
traverse(start=["cls_id"], typ="hier", dir="down", depth=2, ctx="std")  # 280t

# Multi-source traversal (analyze multiple functions together)
traverse(start=["id1","id2","id3"], typ="dep", dir="down", depth=2, ctx="min")  # 420t

# Filtered traversal (Python functions only)
traverse(start=["id"], typ="dep", dir="down", depth=3, filters={"types":["fn"],"files":["**/*.py"]})

# File-based traversal (all dependencies in file)
traverse(start=["src/api/auth.py"], typ="dep", dir="down", depth=2, ctx="min")  # 380t
```

**Response Formats**:
- `fmt="tree"`: Hierarchical text tree (human-readable)
- `fmt="flat"`: List of nodes with relationships (programmatic)
- `fmt="graph"`: Graph structure with edges (for visualization)

---

## Tool 3: batch_files() - Parallel Operations

**Signature**:
```python
batch_files(
    files: list[str],          # File paths (absolute or relative)
    op: str,                   # Operation: "list","overview","validate","stats"
    t: str | list[str] = None, # Type filter for "list" op
    ctx: str = "std",          # Context level
    fmt: str = "auto"          # Format
)
```

**Operations**:
- `op="list"`: All symbols in files (respects `t` filter)
- `op="overview"`: High-level summary (file-level metrics, top symbols)
- `op="validate"`: Parse errors, syntax issues
- `op="stats"`: Line counts, symbol counts, complexity estimates

**Critical Rules**:
- **ALWAYS** use `batch_files()` for 3+ files instead of multiple `query()` calls
- Use `op="overview"` first for exploration, then `op="list"` for details
- 2-3x faster than sequential operations
- Handles up to ~50 files efficiently in one call

**Common Patterns**:

```python
# Get all symbols from multiple files (type-filtered)
batch_files(files=["src/api/auth.py","src/api/users.py"], op="list", t="fn", ctx="std")  # 400t

# Quick file overviews (exploration)
batch_files(files=["src/models/*.py"], op="overview")  # 300t for 6 files

# Validation check (syntax errors)
batch_files(files=["src/**/*.py"], op="validate")  # 150t

# Get file statistics
batch_files(files=["src/api/*.py"], op="stats")  # 200t

# Full symbol listing (minimal context for speed)
batch_files(files=["file1.py","file2.py","file3.py"], op="list", ctx="min")  # 420t

# Class-only listing across modules
batch_files(files=["src/models/*.py"], op="list", t="cls", ctx="std")  # 350t
```

**When to Use**:
- File-level code review
- Module exploration
- Pre-commit validation
- Parallel symbol extraction
- Any operation on 3+ files

---

## Tool 4: aggregate() - Metrics & Analytics

**Signature**:
```python
aggregate(
    metric: str,               # Metric: "count","complexity","doc_coverage","patterns","loc"
    scope: list[str] = None,   # Paths/globs to analyze
    group_by: str = None,      # Group: "file","type","dir","lang"
    filters: dict = None,      # {"types":["fn"],"min_complexity":10}
    fmt: str = "auto"          # Format
)
```

**Metrics**:
- `count`: Symbol counts by type/file/directory
- `complexity`: Cyclomatic complexity analysis (functions only)
- `doc_coverage`: Documentation percentage (functions/classes/methods)
- `patterns`: Pattern frequency (naming conventions, code smells)
- `loc`: Lines of code by file/directory/language

**Critical Rules**:
- Use `aggregate()` before detailed queries - get overview first
- Set `scope` to focus analysis (avoid full-repo metrics when unnecessary)
- Use `filters` to reduce noise (e.g., `min_complexity=10` for complex functions only)
- `group_by` determines result structure

**Common Patterns**:

```python
# Repository overview (counts by type)
aggregate(metric="count", scope=["src/"], group_by="type")  # 200t

# Complexity hotspots (functions with complexity > 10)
aggregate(metric="complexity", scope=["src/"], filters={"min_complexity":10}, group_by="file")  # 450t

# Documentation coverage by module
aggregate(metric="doc_coverage", scope=["src/api/","src/models/"], group_by="dir")  # 320t

# Pattern analysis (naming conventions)
aggregate(metric="patterns", scope=["src/"], filters={"pattern":"^get_.*"})  # 380t

# Lines of code by language
aggregate(metric="loc", scope=["src/","scripts/"], group_by="lang")  # 180t

# File-level complexity (find largest files)
aggregate(metric="complexity", scope=["src/api/*.py"], group_by="file")  # 420t

# Type distribution in directory
aggregate(metric="count", scope=["src/models/"], group_by="type")  # 150t
```

**Response Structure**:
```json
{
  "metric": "complexity",
  "scope": ["src/"],
  "results": [
    {"key": "src/api/auth.py", "value": 145, "details": {"functions": 8, "avg": 18.1}},
    {"key": "src/services/user.py", "value": 89, "details": {"functions": 5, "avg": 17.8}}
  ],
  "summary": {"total": 234, "avg": 17.95, "max": 32, "min": 8}
}
```

---

## Tool 5: diff() - Change Impact Analysis

**Signature**:
```python
diff(
    base: str,                 # Base ref: "HEAD","main","v1.0.0","HEAD~3"
    target: str,               # Target ref: "working","feature-branch","HEAD"
    scope: str = "symbols",    # Scope: "symbols","files","all"
    ctx: str = "std",          # Context level
    impact: bool = True,       # Include impact analysis (dependencies)
    fmt: str = "auto"          # Format
)
```

**References**:
- `"working"`: Current working directory (uncommitted changes)
- `"HEAD"`: Latest commit
- `"main"`,`"develop"`: Branch names
- `"v1.0.0"`,`"v2.3.1"`: Tags
- `"HEAD~3"`: 3 commits back

**Scope Options**:
- `scope="symbols"`: Symbol-level changes (added/modified/deleted functions, classes)
- `scope="files"`: File-level changes (new, modified, deleted files)
- `scope="all"`: Both symbols and files

**Critical Rules**:
- Set `impact=True` to get dependency impact (what else might break)
- Use `scope="symbols"` for code review, `scope="files"` for release notes
- `ctx="min"` for large diffs (100+ changes)
- Always specify both `base` and `target`

**Common Patterns**:

```python
# What changed since last commit? (working directory)
diff(base="HEAD", target="working", scope="symbols", impact=True, ctx="std")  # 650t

# Release notes (tag to tag)
diff(base="v1.0.0", target="v2.0.0", scope="symbols", ctx="min")  # 480t

# Branch comparison (what's in feature branch)
diff(base="main", target="feature/auth-refactor", scope="symbols", impact=True)  # 720t

# Last 5 commits (historical analysis)
diff(base="HEAD~5", target="HEAD", scope="symbols", ctx="min")  # 420t

# File-level changes only (quick overview)
diff(base="HEAD~1", target="HEAD", scope="files")  # 280t

# Full diff with impact (code review)
diff(base="HEAD", target="working", scope="all", impact=True, ctx="std")  # 850t

# Compare branches without impact (fast)
diff(base="develop", target="feature/new", scope="symbols", impact=False, ctx="min")  # 380t
```

**Response Structure**:
```json
{
  "base": "HEAD",
  "target": "working",
  "changes": {
    "added": [{"n":"new_function","t":"fn","f":"src/api/auth.py","l":45}],
    "modified": [{"n":"authenticate","t":"fn","f":"src/api/auth.py","l":23,"changes":["signature","body"]}],
    "deleted": [{"n":"old_function","t":"fn","f":"src/api/auth.py"}]
  },
  "impact": {
    "affected_symbols": [{"n":"verify_token","reason":"calls modified authenticate()"}],
    "risk_score": 7.2
  },
  "stats": {"added":3,"modified":5,"deleted":2}
}
```

---

## Chaining Patterns (Critical for Efficiency)

### Pattern 1: Explore → Detail
```python
# Step 1: Fuzzy search, minimal context (120t)
results = query(q="auth", t="fn", m="f", ctx="min", lim=20)

# Step 2: Get full details for 2 interesting functions (160t)
query(q=["authenticate_user","verify_token"], m="x", ctx="full")

# Total: 280t vs 1600t if you used ctx="full" in step 1
```

### Pattern 2: Aggregate → Batch → Detail
```python
# Step 1: Get file complexity metrics (350t)
complexity = aggregate(metric="complexity", scope=["src/api/"], group_by="file")

# Step 2: Get symbols from top 3 complex files (450t)
batch_files(files=top_3_files, op="list", t="fn", ctx="std")

# Step 3: Analyze specific complex function (80t)
query(q="complex_function_name", m="x", ctx="full")

# Total: 880t vs 3500t if you listed all API files first
```

### Pattern 3: Query → Traverse → Batch
```python
# Step 1: Find entry point (50t)
entry = query(q="main", t="fn", m="x", ctx="min")

# Step 2: Get its dependencies (350t)
deps = traverse(start=[entry["r"][0]["id"]], typ="dep", dir="down", depth=2)

# Step 3: Get details for all dependency files (600t)
batch_files(files=dep_files, op="list", ctx="std")

# Total: 1000t, comprehensive dependency analysis
```

### Pattern 4: Diff → Impact → Fix
```python
# Step 1: What changed? (650t)
changes = diff(base="HEAD", target="working", scope="symbols", impact=True)

# Step 2: Analyze affected functions (250t per ID)
for affected_id in changes["impact"]["affected_symbols"]:
    traverse(start=[affected_id], typ="dep", dir="up", depth=1)

# Step 3: Get full context for fixes (80t per function)
query(q=affected_names, m="x", ctx="full")
```

---

## Token Optimization Rules (Critical)

### Rule 1: Progressive Context
```python
# ✅ CORRECT: Start minimal, expand as needed
query(q="auth", m="f", ctx="min", lim=20)  # 120t
# User interested in one? Get full context
query(q="authenticate_user", m="x", ctx="full")  # 80t
# TOTAL: 200t

# ❌ WRONG: Full context for exploratory search
query(q="auth", m="f", ctx="full", lim=20)  # 1600t
```

### Rule 2: Batch Over Sequential
```python
# ✅ CORRECT: Batch operation
batch_files(files=[...5 files...], op="list", ctx="min")  # 420t, 185ms

# ❌ WRONG: Sequential queries
for file in files:
    query(q="*", f=file, ctx="min")  # 400t, 475ms (2.5x slower)
```

### Rule 3: Filter Early
```python
# ✅ CORRECT: Filter at query time
query(q="*", t="fn", g="src/**/*.py", ctx="std", lim=50)  # 1500t

# ❌ WRONG: Get all, filter manually
query(q="*", ctx="std", lim=500)  # 15000t, then manual filtering
```

### Rule 4: Use Symbol IDs
```python
# ✅ CORRECT: Chain with IDs
result1 = query(q="UserService", t="cls", m="x", ctx="min")  # 30t
traverse(start=[result1["r"][0]["id"]], typ="hier", depth=1)  # 250t
# TOTAL: 280t

# ❌ WRONG: Re-query by file path
result1 = query(q="UserService", t="cls", m="x", ctx="full")  # 85t
traverse(start=["src/services/user.py"], typ="hier", depth=1)  # 250t
# TOTAL: 335t
```

### Rule 5: Aggregate Before Detail
```python
# ✅ CORRECT: Get overview first
aggregate(metric="count", scope=["src/api/"])  # 200t
# Then get details for largest file
batch_files(files=["largest_file.py"], op="list", ctx="std")  # 400t
# TOTAL: 600t

# ❌ WRONG: Get all details first
batch_files(files=[...all 15 API files...], op="list", ctx="std")  # 3500t
```

---

## Quick Decision Tree

**Need to find specific symbol?**
→ `query(q="exact_name", m="x", ctx="std")`

**Exploring unfamiliar code?**
→ `query(q="pattern", m="f", ctx="min", lim=20)` then expand

**Need to understand dependencies?**
→ `query()` to get ID, then `traverse(start=[id], typ="dep")`

**Working with multiple files?**
→ `batch_files(files=[...], op="list")` always

**Need code metrics?**
→ `aggregate(metric=..., scope=[...])` first

**Analyzing changes?**
→ `diff(base=..., target=..., impact=True)`

**Need class hierarchy?**
→ `query()` to get class ID, then `traverse(typ="hier")`

**Code review scenario?**
→ `diff()` → `traverse()` on affected symbols → `query()` for details

---

## Performance Targets

| Query Type | Target Time | Max Results |
|------------|-------------|-------------|
| Exact match | <100ms | 1-5 |
| Fuzzy search | <200ms | 10-50 |
| Batch files (5) | <250ms | 5 files |
| Traverse (depth 2) | <200ms | 10-30 nodes |
| Aggregate | <400ms | 100+ symbols |
| Diff | <600ms | 50+ changes |

**If operations exceed these times**:
- Reduce `depth` in `traverse()`
- Increase filters in `query()`
- Use `ctx="min"` for large result sets
- Reduce `scope` in `aggregate()`

---

## Critical Anti-Patterns

### ❌ NEVER Do This:
```python
# 1. Full context for large result sets
query(q="get_", m="f", ctx="full", lim=100)  # 8000t

# 2. Sequential file operations
for file in files: query(q="*", f=file)  # Slow, high token cost

# 3. Re-query by name when you have ID
query(q="UserService")  # Already have this result
traverse(start=["src/services/user.py"])  # Should use ID

# 4. No filters on broad queries
query(q="*", ctx="std", lim=500)  # 15000t

# 5. Requesting relationships when not needed
query(q="UserService", rel=True, ctx="full")  # 350t vs 85t
```

### ✅ ALWAYS Do This:
```python
# 1. Progressive context disclosure
query(q="pattern", ctx="min") → query(q="exact", ctx="full")

# 2. Batch operations
batch_files(files=[...])

# 3. Chain with IDs
id = query(...)["r"][0]["id"]
traverse(start=[id])

# 4. Filter at query time
query(q="*", t="fn", g="**/*.py", lim=50)

# 5. Request relationships only when needed
query(q="UserService", rel=False, ctx="std")
# Then: traverse() if relationships needed
```

---

## Summary: Core Principles

1. **Start Minimal**: Use `ctx="min"` by default, expand progressively (60-80% token savings)
2. **Batch Always**: 3+ files = use `batch_files()` (70-85% token savings)
3. **Chain with IDs**: Use symbol IDs, not names (15-25% faster)
4. **Filter Early**: Apply filters at query time (50-70% token savings)
5. **Aggregate First**: Get metrics before details (avoid over-fetching)

**Overall Efficiency**: 94% fewer API calls, 77% token reduction vs sequential operations.

---

## Type Abbreviations Reference

| Code | Symbol Type |
|------|-------------|
| `fn` | Function |
| `cls` | Class |
| `mth` | Method |
| `var` | Variable |
| `imp` | Import |
| `tgt` | Makefile target |
| `svc` | Docker Compose service |
| `stg` | Docker build stage |
| `als` | Bash alias |
| `exp` | Bash export |
| `arg` | Docker ARG |
| `env` | Docker ENV |
| `vol` | Docker volume |
| `net` | Docker network |
| `hdr` | Markdown heading |
