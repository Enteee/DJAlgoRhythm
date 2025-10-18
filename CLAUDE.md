# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Environment

This project uses **devenv** (Nix-based development environment) for consistent development setup across machines.

### Essential Commands

- `devenv shell` - Enter the development environment
- `devenv-help` - Display available helper scripts
- `git lfs pull` - Pull Git LFS artifacts (done automatically on shell init)

### Environment Setup

- The project automatically initializes Git LFS and pulls artifacts when entering an interactive shell
- Locale is set to C.UTF-8 for consistent behavior
- DO_NOT_TRACK=1 is set by default
- Poetry keyring backend is disabled to avoid keyring queries

## Development Tools

The devenv provides:
- Git and Git LFS
- Claude Code CLI
- VS Code with pre-configured extensions (interactive mode only)
- Nix language support

## Code Quality & Git Hooks

Pre-commit hooks are automatically configured and include:
- **dos2unix** - Convert line endings (excludes assets)
- **trim-trailing-whitespace** - Remove trailing whitespace
- **nixfmt-rfc-style** - Format Nix files
- **shellcheck** - Shell script analysis with extended checks
- **hadolint** - Dockerfile linting
- **markdownlint** - Markdown formatting (120 char line limit)
- **yamllint** - YAML validation (excludes pnpm-lock.yaml, charts/)
- **check-json/check-toml** - JSON/TOML validation
- **trufflehog/ripsecrets** - Secret detection
- **typos** - Spell checking (excludes SVG files)

### Mandatory Code Quality Checks

**CRITICAL**: After making ANY code changes, you MUST run `make check` before proceeding or considering the task complete. This command runs:

1. **make fmt** - Format all Go code with `go fmt` and `gofmt -s`
2. **make vet** - Run `go vet` to catch common Go mistakes
3. **make lint** - Run `golangci-lint` for comprehensive linting
4. **make test** - Run all tests with race detection
5. **make build** - Ensure the code builds successfully

**Never skip this step.** All changes must pass these checks. If any check fails, fix the issues before proceeding.

## Go Development Best Practices

### Code Quality Standards
- **No magic numbers**: Use named constants for all numeric literals
- **No unused parameters**: Rename unused parameters to `_` (underscore)
- **Line length**: Keep lines under 140 characters, break long lines appropriately
- **String constants**: Repeated strings (2+ occurrences) must be constants
- **HTTP requests**: Use `http.NoBody` instead of `nil` for requests without body
- **Error handling**: Always handle errors explicitly, never ignore them
- **Context usage**: Pass context through function calls for cancellation/timeout

### Configuration Management
**CRITICAL**: When introducing new configuration options:

1. **Add to config structs** - Update the relevant config struct in `internal/core/config.go`
2. **Update .env.example** - ALWAYS add the new option to `.env.example` with:
   - Clear documentation comment explaining the option
   - Sensible default value or example
   - Required/optional status indication
3. **Add CLI flags** - Add corresponding command-line flags in `cmd/whatdj/main.go`
4. **Update README.md** - Add the new option to the configuration table in README.md
5. **Test the option** - Ensure the new configuration works with both environment variables and CLI flags

**Never add a config option without updating `.env.example` - this is the primary reference for users.**

### Makefile Usage
The project includes a comprehensive Makefile with targets for:
- `make build` - Build the binary
- `make test` - Run tests with race detection
- `make fmt` - Format code
- `make vet` - Run go vet
- `make lint` - Run golangci-lint
- `make check` - **Run all quality checks (fmt, vet, lint, test, build)**
- `make clean` - Clean build artifacts
- `make run` - Build and run the application

Use `make help` to see all available targets.

## Project Structure

This is a fresh repository with minimal structure. The project is set up with:
- Nix-based development environment via devenv
- Comprehensive pre-commit hook setup for code quality
- Git LFS integration for large file handling
- Claude Code integration for AI-assisted development

The repository appears to be in its initial setup phase with only the base devenv configuration committed.