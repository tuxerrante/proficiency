# Contributing to Proficiency

We welcome contributions! This document explains the process.

## How to Contribute

1. **Report bugs** — Open a [GitHub issue](https://github.com/tuxerrante/proficiency/issues) with steps to reproduce
2. **Suggest features** — Open an issue using the feature request template
3. **Submit code** — Fork, branch, and open a pull request

## Pull Request Process

1. Create a feature branch from `main` (naming: `issue/<number>-<description>`)
2. Write tests for new functionality (TDD preferred)
3. Ensure all checks pass:
   ```bash
   make lint       # golangci-lint, 0 issues required
   make coverage   # must stay above 80%
   go test -race ./...
   ```
4. Use [conventional commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `test:`, `docs:`, `ci:`, `refactor:`
5. Open a PR against `main` — squash merge is the default

## Coding Standards

- Follow the [Go development guidelines](https://raw.githubusercontent.com/tuxerrante/llm-prompt-template/refs/heads/main/backend/go.md)
- Format with `gofumpt` (via `golangci-lint fmt ./...`)
- All linters enabled by default in `.golangci.yml` must pass
- Prefer table-driven tests with `t.Run` subtests
- Use `httptest.NewServer` for HTTP tests, `t.TempDir()` for filesystem tests

## Development Setup

```bash
# Clone and build
git clone https://github.com/tuxerrante/proficiency.git
cd proficiency
make build

# Run the test suite
make test

# Run E2E tests (starts a local stress server)
make e2e
```

## License

By contributing, you agree that your contributions will be licensed under the project's [Business Source License 1.1](LICENSE).
