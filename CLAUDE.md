# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Environment

This project uses **devenv** (Nix-based development environment) for consistent development setup across machines.

### Claude Code Settings

This repository includes Claude Code settings in the `.claude/` directory that control what operations Claude can perform.

**Settings Structure:**

- **`.claude/settings.json`** - Base permissions for all environments (local + CI/CD)
- **`.claude/settings.local.json`** - Your local overrides (gitignored)
- **`.claude/settings.local.json.example`** - Template for creating local settings

**Precedence Hierarchy:**

```text
settings.local.json → settings.json → ~/.claude/settings.json
(highest)                              (lowest)
```

Settings are automatically merged, with local settings overriding base settings.

**Creating Local Overrides:**

If you need additional permissions for local development:

```bash
cp .claude/settings.local.json.example .claude/settings.local.json
# Edit .claude/settings.local.json to add your permissions
```

Your local settings are gitignored and won't affect other developers or CI/CD.

**Documentation:** See `.claude/README.md` for detailed information about the settings system.

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

**CRITICAL**: After making ANY code changes, you MUST run `make check`
before proceeding or considering the task complete. This command runs:

1. **make fmt** - Format all Go code with `go fmt` and `gofmt -s`
2. **make vet** - Run `go vet` to catch common Go mistakes
3. **make lint** - Run `golangci-lint` for comprehensive linting
4. **make test** - Run all tests with race detection
5. **make build** - Ensure the code builds successfully

**Never skip this step.** All changes must pass these checks. If any check fails, fix the issues before proceeding.

### Development Commands

**Running the application:**

```bash
# Quick run with test credentials (disabled integrations)
env DJALGORHYTHM_LANGUAGE=en go run \
  ./cmd/djalgorhythm \
    --telegram-enabled=false \
    --spotify-client-id=test \
    --spotify-client-secret=test \
    --spotify-playlist-id=test

# Run with debug logging
go run ./cmd/djalgorhythm --log-level=debug

# Run with custom config file
go run ./cmd/djalgorhythm --config=myconfig.env
```

**Testing specific components:**

```bash
# Test a specific package
go test ./internal/flood/
go test ./internal/chat/telegram/
go test ./internal/core/

# Run tests with verbose output
go test -v ./internal/core/

# Test with coverage
go test -cover ./...
go tool cover -html=coverage.out  # After running with -coverprofile=coverage.out
```

**Debugging and troubleshooting:**

```bash
# Check application help and all CLI flags
go run ./cmd/djalgorhythm --help

# Validate configuration without running
go run ./cmd/djalgorhythm --config=.env --help

# Test with different languages
env DJALGORHYTHM_LANGUAGE=ch_be go run ./cmd/djalgorhythm --telegram-enabled=false
```

## Go Development Best Practices

### Code Quality Standards

This project enforces strict code quality through golangci-lint with
comprehensive linter configuration. All code must satisfy these requirements:

#### Documentation & Comments

- **godot**: All comments must end with a period (`.`)
  - Example: `// ProcessMessage handles incoming messages.` ✓
  - Example: `// ProcessMessage handles incoming messages` ✗
- Comments should start with the function/type name they document
- Use complete sentences for all public function/type comments

#### Complexity Management

- **gocyclo**: Cyclomatic complexity must not exceed 15
  - Cyclomatic complexity measures independent paths through code
  - If exceeded, refactor by extracting helper functions or simplifying conditional logic

- **gocognit**: Cognitive complexity should be reasonable
  - Cognitive complexity measures how hard code is to understand
  - Break down complex functions into smaller, focused helpers with clear names
  - Reduce nested conditionals and loops

- **cyclop**: Package-level complexity limit is 10
  - Entire packages should maintain low overall complexity

- **nestif**: Avoid deeply nested if statements
  - Maximum nesting depth should be limited
  - Use early returns to reduce nesting
  - Extract complex conditionals into well-named boolean functions

#### Magic Numbers & Constants

- **mnd**: No magic numbers in code
  - All numeric literals (except 0, 1, 0.0, 1.0) must be named constants
  - Applies to: arguments, case statements, conditions, operations, returns, assignments
  - Example: `const maxRetries = 3` then use `maxRetries` in code

- **goconst**: Repeated strings (2+ occurrences) must be constants
  - Minimum string length: 2 characters
  - Prevents string duplication and typos

#### Error Handling

- **errcheck**: Never ignore errors
  - All error returns must be checked explicitly
  - Use `_ = func()` with comment if intentionally ignoring (rare cases only)

- **errorlint**: Use modern Go 1.13+ error wrapping
  - Use `fmt.Errorf("context: %w", err)` for error wrapping
  - Check for `errors.Is()` and `errors.As()` usage where appropriate

#### Code Structure

- **funlen**: Functions should not exceed 100 lines or 50 statements
  - Long functions are hard to test and understand
  - Extract logical blocks into helper functions

- **dupl**: Avoid code duplication (threshold: 100 tokens)
  - Extract common logic into shared functions

- **lll**: Line length must not exceed 140 characters
  - Break long lines appropriately at logical boundaries

#### Other Quality Standards

- **No unused parameters**: Rename unused parameters to `_` (underscore)
- **HTTP requests**: Use `http.NoBody` instead of `nil` for requests without body
- **Context usage**: Pass context through function calls for cancellation/timeout
- **govet**: Enable shadow variable detection to catch accidental variable shadowing

### Writing Linter-Compliant Code

When writing new code or refactoring existing code:

1. **Start simple**: Write the logic first, then refactor to meet complexity limits
2. **Extract early**: If a function grows beyond 50 lines, look for extraction opportunities
3. **Name well**: Helper functions should have clear, descriptive names explaining their purpose
4. **Document everything**: Public functions, types, and constants need godoc comments with periods
5. **Test helpers**: Mark test helper functions with `t.Helper()` for better error reporting
6. **Iterate**: Run `make lint` frequently during development to catch issues early

### Configuration Management

**CRITICAL**: When introducing new configuration options:

1. **Add to config structs** - Update the relevant config struct in `internal/core/config.go`
2. **Update .env.example** - ALWAYS add the new option to `.env.example` with:
   - Clear documentation comment explaining the option
   - Sensible default value or example
   - Required/optional status indication
3. **Add CLI flags** - Add corresponding command-line flags in `cmd/djalgorhythm/main.go`
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

## Project Architecture

DJAlgoRhythm is an AI-powered chat-to-Spotify DJ bot built with Go. Key architectural components:

### Core Architecture

- **`cmd/djalgorhythm/main.go`** - Application entry point using Cobra CLI framework
- **`internal/core/dispatcher.go`** - Central message dispatcher that coordinates all components
- **`internal/core/config.go`** - Configuration management with environment variables and CLI flags
- **`internal/chat/`** - Unified chat frontend interface with platform-specific implementations
- **`internal/spotify/`** - Spotify Web API client for playlist management
- **`internal/llm/`** - LLM provider abstraction (OpenAI primary, others experimental)

### Message Processing Flow

1. **Chat Frontend** → receives messages from Telegram/other platforms
2. **Dispatcher** → processes messages and determines track requests
3. **LLM Provider** → disambiguates natural language requests
4. **Spotify Client** → adds tracks to playlist
5. **Dedup Store** → prevents duplicate additions using Bloom filters + LRU cache

### Key Components

- **Shadow Queue** (`internal/core/shadow_queue.go`) - Tracks queued songs for reliable state management
- **Queue Manager** (`internal/core/queue_manager.go`) - Maintains continuous playback by managing queue ahead duration
- **Approval Handler** (`internal/core/approval_handler.go`) - Manages user confirmations via reactions/buttons
- **Flood Protection** (`internal/flood/`) - Anti-spam protection with sliding window rate limiting
- **Internationalization** (`internal/i18n/`) - Multi-language support (English, Swiss German)

### Testing Strategy

- **Unit tests** for all core components (use `go test ./...`)
- **Integration tests** for chat frontends with disabled configurations
- **Race detection** enabled by default in `make test`
- **Test-specific patterns**: Mock external dependencies, test error conditions

## Important Implementation Details

### LLM Provider Support Status

- **OpenAI**: Fully implemented (`internal/llm/openai.go`)
- **Anthropic/Ollama**: Planned but not yet implemented (only interfaces exist)
- **Current Reality**: Only OpenAI provider works; others will return "not implemented" errors

### Configuration Validation

- All configuration goes through **dual validation**: environment variables AND CLI flags
- USE CLI flags in `cmd/djalgorhythm/main.go` as the authoritative source for configuration options
- `.env.example` must match CLI flags.
- **Required fields**: Spotify credentials, Telegram bot token (if enabled), LLM provider

### Message Processing Pipeline

1. **Frontend Layer** - Chat platform abstraction (`internal/chat/frontend.go`)
2. **Dispatcher** - Central coordinator with context management and concurrent safety
3. **Message Contexts** - Track ongoing conversations with users (`MessageContext` in dispatcher)
4. **Shadow Queue** - Maintains reliable queue state separate from Spotify's queue
5. **Approval Flow** - Reaction-based confirmations with configurable timeouts

### Concurrency Patterns

- **Context-based cancellation** throughout the application
- **Mutex protection** for shared state (queue, contexts, admin warnings)
- **Graceful shutdown** with 30-second timeout and 2-second delay
- **Race-safe operations** for all map access patterns

### Error Handling Strategy

- **Structured logging** with zap logger throughout
- **Context propagation** for request tracing
- **Fallback behaviors** when external services fail
- **User-friendly error messages** via i18n system
