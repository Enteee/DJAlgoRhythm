# Claude Code Settings

This directory contains Claude Code settings configuration that controls what operations Claude can perform in this repository.

## Files

- **`settings.json`** - Base shared settings for all environments (local development + CI/CD)
- **`settings.local.json.example`** - Template for local developer overrides
- **`settings.local.json`** - Your local settings (gitignored, created by you)

## Settings Hierarchy

Claude Code uses an automatic precedence system (no file includes needed):

```
Local Settings (.claude/settings.local.json)    ← Highest precedence (your machine only)
                ↓
Shared Settings (.claude/settings.json)         ← Base config (committed to repo)
                ↓
User Settings (~/.claude/settings.json)         ← Lowest precedence (global defaults)
```

Settings are automatically merged, with higher precedence files overriding lower ones.

## How It Works

### In CI/CD

- Uses only `settings.json` (no `.local.json` exists in CI)
- Provides minimal permissions needed for `make check` and other automated tasks
- Prevents dangerous operations like unrestricted file deletion

### In Local Development

- Uses `settings.json` + `settings.local.json` merged automatically
- You can add local-only permissions without affecting CI or other developers
- Your `.local.json` is gitignored to keep personal preferences private

## Creating Local Overrides

1. Copy the example file:
   ```bash
   cp .claude/settings.local.json.example .claude/settings.local.json
   ```

2. Edit `.claude/settings.local.json` to add your preferences:
   ```json
   {
     "$schema": "https://raw.githubusercontent.com/anthropics/claude-code/main/schemas/settings.schema.json",
     "permissions": {
       "allow": [
         "Bash(docker:*)",
         "Bash(npm:*)"
       ],
       "deny": [
         "Bash(rm -rf:*)"
       ],
       "ask": [
         "Bash(git push:*)"
       ]
     }
   }
   ```

3. Your local settings will automatically merge with the base settings

## Permission Patterns

### Allow Patterns

Grant specific permissions to Claude:

```json
"allow": [
  "Bash(make:*)",              // Allow all make commands
  "Bash(go test:*)",           // Allow all go test commands
  "Bash(rm -f coverage.out)",  // Allow only specific file deletion
  "Read(/nix/store/**)",       // Allow reading from Nix store
  "WebFetch(domain:github.com)" // Allow fetching from GitHub
]
```

### Deny Patterns

Override allows from lower precedence files:

```json
"deny": [
  "Bash(rm -rf /)",            // Prevent root deletion
  "Bash(git push --force:*)"   // Prevent force push
]
```

### Ask Patterns

Require confirmation before executing:

```json
"ask": [
  "Bash(git push:*)",          // Confirm before pushing
  "Write(**/*.env)"            // Confirm before writing .env files
]
```

## Security Best Practices

1. **Principle of Least Privilege**: Only grant permissions that are actually needed
2. **Specific over Broad**: Use `Bash(rm -f coverage.out)` instead of `Bash(rm:*)`
3. **Audit Regularly**: Review `settings.json` when adding new development tools
4. **Use Deny Patterns**: Explicitly deny dangerous operations in your local settings

## Shared Settings (settings.json)

The base `settings.json` provides permissions for:

- **Go Development**: `go build`, `go test`, `go fmt`, `go vet`, etc.
- **Linting**: `golangci-lint`, `staticcheck`, `gosec`
- **Version Control**: `git diff`, `git add`, `git push`, `git status`, etc.
- **Build Tools**: `make`, `goreleaser`, `pre-commit`
- **File Operations**: Specific safe operations needed by Makefile targets
- **Web Resources**: Documentation sites (GitHub, Go pkg docs, Spotify API docs)

## Documentation

For more information, see:

- [Official Claude Code Settings Documentation](https://docs.claude.com/en/docs/claude-code/settings)
- [Settings Schema](https://raw.githubusercontent.com/anthropics/claude-code/main/schemas/settings.schema.json)

## Troubleshooting

### "Permission denied" errors

If Claude cannot execute a command:

1. Check if the command is in `settings.json` allow list
2. Add it to your `settings.local.json` if needed for local development
3. For CI/CD needs, update `settings.json` (and commit the change)

### Settings not taking effect

1. Ensure your JSON syntax is valid (use a JSON validator)
2. Check file names are correct: `settings.json` and `settings.local.json`
3. Restart your Claude Code session if needed

### Overly restrictive permissions

If you need broader permissions locally but don't want to affect CI:

1. Add them to `settings.local.json` (not `settings.json`)
2. Your local file will override the base settings
3. Other developers and CI remain unaffected
