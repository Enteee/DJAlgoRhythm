---
description: Run quality checks, fix issues, commit, push, and manage PR - "Looks Good To Merge"
allowed-tools:
  - Bash
---

# LGTM - Looks Good To Merge

This command automates the complete PR workflow for getting code ready to merge.

## 🚨 CRITICAL SAFETY RULE 🚨

**NEVER EVER push to the main branch!**

This command includes multiple safety checks to ensure you're on a feature branch before pushing.
If at ANY point you detect you're on the main branch when attempting to push, ABORT IMMEDIATELY.

## Workflow Steps

You MUST follow these steps in order:

### Step 1: Run Quality Checks and Fix Issues

1. Run `make check` to verify code quality
2. If `make check` fails:
   - Analyze the errors and warnings
   - Fix all issues one by one
   - Re-run `make check` after each fix
   - Continue until `make check` passes completely
3. If `make check` passes on first run, proceed to next step

**IMPORTANT**: Do not proceed to step 2 until `make check` passes without any errors.

### Step 2: Check Current Branch and Create Feature Branch If Needed

**CRITICAL**: NEVER EVER push to the main branch. This step MUST verify we're on a feature branch.

1. Run `git branch --show-current` to get the current branch name
2. If the current branch is `main`:
   - **STOP**: You CANNOT proceed on main branch
   - Analyze the uncommitted changes with `git diff` and `git status`
   - Generate a descriptive branch name based on the changes (format: `feature/short-description` or `fix/short-description`)
   - Create and checkout the new branch: `git checkout -b <branch-name>`
   - Verify the branch was created: `git branch --show-current`
   - Only proceed if confirmed NOT on main branch
3. If already on a feature branch (NOT main):
   - Verify it's truly not main: `git branch --show-current`
   - Confirm before proceeding to next step

**IMPORTANT**: If at ANY point you detect you're on main branch in later steps, ABORT IMMEDIATELY and inform the user.

### Step 3: Check for Changes and Create Commit

1. Run `git status` to check for uncommitted changes
2. If there are no changes to commit:
   - Inform the user that there are no changes
   - Skip to step 5 (PR management)
3. If there are changes:
   - Run `git diff` and `git status` to analyze the changeset
   - Generate a meaningful, detailed commit message following the project's convention:
     - Use conventional commit format (feat:, fix:, refactor:, etc.)
     - Include comprehensive description
     - Add technical details in the body
     - End with Claude Code signature:

       ```text
       🤖 Generated with [Claude Code](https://claude.com/claude-code)

       Co-Authored-By: Claude <noreply@anthropic.com>
       ```

   - Stage all changes: `git add -A`
   - Create the commit with the generated message

### Step 4: Push Changes to Remote

**CRITICAL SAFETY CHECK**: Before pushing, verify we are NOT on main branch!

1. Run `git branch --show-current` to verify current branch
2. **ABORT IF ON MAIN**: If the current branch is `main`, STOP IMMEDIATELY and inform the user that pushing to main is forbidden
3. If on a feature branch (confirmed NOT main):
   - Run `git status` to check if branch tracks remote
   - If branch doesn't track remote or is ahead:
     - Push with: `git push -u origin <branch-name>` (or just `git push` if already tracking)
   - NEVER use `--force` or `--force-with-lease` to main branch

### Step 5: Manage Pull Request

1. Check if a PR exists for this branch:
   - Run: `gh pr view --json number,url 2>/dev/null || echo "NO_PR"`
2. If a PR exists (command returns JSON with PR number):
   - Get the full diff against origin/main: `git diff origin/main`
   - Analyze the complete changeset
   - Generate a comprehensive PR description including:
     - Summary of changes
     - Detailed breakdown by category
     - Technical details
     - Benefits
     - Testing notes
     - Any breaking changes
   - Update PR description: `gh pr edit <number> --body "<description>"`
   - Inform the user with the PR URL
3. If no PR exists:
   - Analyze the complete changeset against origin/main
   - Generate PR title from commit message or changeset
   - Generate comprehensive PR description (same format as above)
   - Create PR: `gh pr create --title "<title>" --body "<description>"`
   - Inform the user with the new PR URL

## Important Notes

- **Always use TodoWrite tool** to track progress through the steps
- **Never skip steps** - each must complete successfully before proceeding
- **If any step fails**, inform the user and stop (do not proceed to next steps)
- **Use heredocs for multi-line commit/PR messages** to preserve formatting
- **Check git status** before making assumptions about repository state
- **Be thorough in PR descriptions** - include all relevant details about changes

## Example Branch Naming

- `fix/docker-build-error` - for bug fixes
- `feature/add-lgtm-command` - for new features
- `refactor/simplify-makefile` - for refactoring
- `docs/update-readme` - for documentation changes

## Error Handling

- **If attempting to push to main branch**: ABORT IMMEDIATELY with error message explaining you cannot push to main
- If `make check` cannot be fixed automatically, explain the issue to the user
- If git operations fail, show the error and ask for user intervention
- If `gh` command is not available, inform the user they need GitHub CLI

## Safety Checks Summary

The command performs branch verification at multiple points:

1. **Step 2**: Before creating commit - ensures on feature branch
2. **Step 4**: Before pushing - double-checks NOT on main branch
3. **Throughout**: If main branch detected at any point, abort with error
