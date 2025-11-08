# Contributing to go-hookd

Thank you for your interest in contributing to go-hookd! This document provides guidelines and instructions for contributing to the project.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Development Workflow](#development-workflow)
- [Coding Standards](#coding-standards)
- [Testing Requirements](#testing-requirements)
- [Documentation](#documentation)
- [Pull Request Process](#pull-request-process)
- [Project Structure](#project-structure)

## Code of Conduct

This project adheres to a code of conduct that expects all contributors to be respectful, constructive, and professional. Please treat all contributors with respect and create a positive environment for collaboration.

## Getting Started

1. **Fork the repository** on GitHub
2. **Clone your fork** locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/go-hookd.git
   cd go-hookd
   ```
3. **Add upstream remote**:
   ```bash
   git remote add upstream https://github.com/itsatony/go-hookd.git
   ```
4. **Create a branch** for your changes:
   ```bash
   git checkout -b feature/your-feature-name
   ```

## Development Setup

### Prerequisites

- Go 1.24 or higher
- PostgreSQL 15+ (for integration tests)
- Docker and Docker Compose (recommended)
- Make (optional, for convenience commands)

### Installing Dependencies

```bash
go mod download
go mod verify
```

### Setting Up the Database

```bash
# Start PostgreSQL with Docker Compose
docker-compose up -d

# Bootstrap the database
./scripts/db-dev.sh bootstrap

# Or run migrations manually
psql -h localhost -p 54321 -U hookd -d hookd -f migrations/postgres/000001_create_tables.up.sql
```

### Running Tests

```bash
# Unit tests
go test ./internal/ -v

# With race detector
go test -race ./internal/

# With coverage
go test -cover ./internal/

# Generate coverage report
go test -coverprofile=coverage.out ./internal/
go tool cover -html=coverage.out
```

### Running Benchmarks

```bash
# All benchmarks
go test -bench=. -benchmem ./internal/ -run=^$

# Specific benchmark
go test -bench=BenchmarkQueueDelivery -benchmem ./internal/ -run=^$
```

## Development Workflow

1. **Sync with upstream**:
   ```bash
   git fetch upstream
   git rebase upstream/main
   ```

2. **Make your changes** following the coding standards

3. **Run tests** to ensure nothing breaks:
   ```bash
   go test ./internal/
   go test -race ./internal/
   ```

4. **Run linters**:
   ```bash
   go vet ./...
   gofmt -s -w .
   ```

5. **Commit your changes**:
   ```bash
   git add .
   git commit -m "feat: add new feature"
   ```

6. **Push to your fork**:
   ```bash
   git push origin feature/your-feature-name
   ```

7. **Create a Pull Request** on GitHub

## Coding Standards

### General Principles

- **Zero Magic Strings**: Use constants for all string literals
- **Type Safety**: Prefer strongly-typed interfaces over `interface{}`
- **Error Handling**: Return errors, don't panic
- **Documentation**: All exported functions must have godoc comments
- **Testing**: Aim for 70%+ test coverage on new code

### Code Style

- Follow **standard Go formatting** (`gofmt`)
- Use **meaningful variable names** (no single letters except loop indices)
- Keep functions **focused and small** (< 50 lines ideal)
- Prefer **composition over inheritance**
- Use **interfaces** for abstraction

### Naming Conventions

```go
// Constants: UPPER_SNAKE_CASE or PascalCase
const DefaultMaxRetries = 3
const EventTopicDeliveryQueued = "delivery.queued"

// Variables: camelCase
var maxRetries int

// Functions: PascalCase (exported), camelCase (unexported)
func CreateSubscription() {}
func normalizeURL() string {}

// Types: PascalCase
type Subscription struct {}
type deliveryWorker struct {}
```

### Error Handling

```go
// Always return errors, never panic
func DoSomething() error {
    if err := validate(); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }
    return nil
}

// Use custom error types for specific cases
func GetSubscription(id string) (*Subscription, error) {
    sub, err := repo.Get(id)
    if err != nil {
        return nil, fmt.Errorf("failed to get subscription: %w", err)
    }
    if sub == nil {
        return nil, NewSubscriptionNotFoundError(id)
    }
    return sub, nil
}
```

### Comments

```go
// Package-level comment
// Package internal provides webhook management...

// Function comment with description, parameters, return values, and examples
// CreateSubscription creates a new webhook subscription.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: Subscription creation request
//
// Returns the created subscription or an error.
//
// Example:
//
//	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
//	    TenantID: "tenant_123",
//	    URL: "https://example.com/webhook",
//	})
func CreateSubscription(ctx context.Context, req *CreateSubscriptionRequest) (*Subscription, error) {
    // Implementation
}
```

## Testing Requirements

### Test Coverage

- **New features**: Minimum 70% coverage
- **Bug fixes**: Add regression tests
- **Refactoring**: Maintain or improve existing coverage

### Test Structure

```go
func TestFeatureName(t *testing.T) {
    t.Run("success case description", func(t *testing.T) {
        // Arrange
        config := NewConfig("postgres://localhost/test")
        repo := NewMockRepository()
        manager, _ := NewManager(config, repo)

        // Act
        result, err := manager.DoSomething(ctx, input)

        // Assert
        require.NoError(t, err)
        assert.Equal(t, expected, result)
    })

    t.Run("error case description", func(t *testing.T) {
        // Test error handling
    })
}
```

### Test Categories

1. **Unit Tests**: Test individual functions in isolation
2. **Integration Tests**: Test with real database (use `-tags=integration`)
3. **Benchmarks**: Measure performance characteristics
4. **Table-Driven Tests**: For testing multiple scenarios

### Example Test

```go
func TestQueueDelivery(t *testing.T) {
    tests := []struct {
        name        string
        setup       func(*Manager)
        req         *QueueDeliveryRequest
        expectError bool
    }{
        {
            name: "valid delivery",
            req: &QueueDeliveryRequest{
                SubscriptionID: "sub_123",
                EventType:      "user.created",
                Payload:        map[string]interface{}{"test": "data"},
            },
            expectError: false,
        },
        {
            name: "invalid request",
            req: &QueueDeliveryRequest{
                SubscriptionID: "", // Missing
                EventType:      "user.created",
            },
            expectError: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

## Documentation

### Required Documentation

1. **Godoc comments** for all exported types, functions, and constants
2. **README** updates for new features
3. **Examples** for complex features
4. **Migration guides** for breaking changes

### Documentation Style

- Use **complete sentences** with proper punctuation
- Include **examples** where helpful
- Document **errors** that can be returned
- Explain **non-obvious behavior**

## Pull Request Process

### Before Submitting

- [ ] All tests pass (`go test ./...`)
- [ ] No race conditions (`go test -race ./...`)
- [ ] Code is formatted (`gofmt -s -w .`)
- [ ] Code is vetted (`go vet ./...`)
- [ ] Documentation is updated
- [ ] Commit messages are descriptive

### PR Title Format

Use conventional commits format:

```
feat: add new feature
fix: correct bug in delivery processing
docs: update README with examples
test: add tests for subscription CRUD
refactor: simplify worker pool implementation
perf: optimize delivery queueing
chore: update dependencies
```

### PR Description Template

```markdown
## Description
Brief description of what this PR does.

## Changes
- Change 1
- Change 2
- Change 3

## Testing
How was this tested?

## Breaking Changes
Any breaking changes? (yes/no)
If yes, describe migration path.

## Checklist
- [ ] Tests pass
- [ ] Documentation updated
- [ ] No breaking changes (or migration guide provided)
```

### Review Process

1. **Automated checks** must pass (tests, linting)
2. **Code review** by at least one maintainer
3. **Address feedback** in follow-up commits
4. **Squash commits** if requested
5. **Merge** when approved

## Project Structure

```
go-hookd/
├── internal/              # Core implementation
│   ├── hookd.config.go         # Configuration
│   ├── hookd.manager.go        # Manager and worker pool
│   ├── hookd.subscription.go   # Subscription CRUD
│   ├── hookd.events.go         # Delivery queueing
│   ├── hookd.models.go         # Domain models
│   ├── hookd.repository.*.go  # Data persistence
│   ├── hookd.errors.go         # Error types
│   ├── hookd.constants.go      # Constants
│   ├── hookd.utils.go          # Utilities
│   └── *_test.go              # Tests
├── migrations/           # Database migrations
│   └── postgres/
├── examples/            # Example applications
│   ├── basic/
│   └── http-server/
├── scripts/             # Helper scripts
├── docs/                # Additional documentation
├── README.md
├── CONTRIBUTING.md
└── docker-compose.yml
```

## Types of Contributions

We welcome various types of contributions:

- **Bug fixes**: Fix issues in existing functionality
- **New features**: Add new capabilities
- **Performance improvements**: Optimize critical paths
- **Documentation**: Improve or add documentation
- **Tests**: Increase test coverage
- **Examples**: Add usage examples
- **Bug reports**: Report issues with reproduction steps

## Getting Help

- **GitHub Issues**: For bugs and feature requests
- **GitHub Discussions**: For questions and discussions
- **Pull Request Comments**: For code-specific questions

## License

By contributing to go-hookd, you agree that your contributions will be licensed under the MIT License.

---

Thank you for contributing to go-hookd! 🎉
