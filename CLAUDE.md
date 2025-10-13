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

## Project Structure

This is a fresh repository with minimal structure. The project is set up with:
- Nix-based development environment via devenv
- Comprehensive pre-commit hook setup for code quality
- Git LFS integration for large file handling
- Claude Code integration for AI-assisted development

The repository appears to be in its initial setup phase with only the base devenv configuration committed.