# Fix Issue #320: Windows HOST_ORIGINAL_HOME path format causing manager-agent startup failure

## Issue Overview

- **Issue Number**: #320
- **Issue Type**: bug
- **Repository**: higress-group/hiclaw
- **Status**: In Progress

## Problem Description

On Windows 11 + Docker Desktop, after installing hiclaw-manager using `install.ps1`, the manager-agent service repeatedly fails to start.

**Error Log:**
```
ln: failed to create symbolic link 'D:\Users\xxx': No such file or directory
```

**Root Cause:**
The `install.ps1` script sets `HOST_ORIGINAL_HOME=D:\Users\xxx` (Windows path format), which Linux containers cannot recognize.

**Suggested Fix:**
In Windows environments, either don't set `HOST_ORIGINAL_HOME`, or convert it to Linux path format.

## Related Links

- Issue URL: https://github.com/higress-group/hiclaw/issues/320

## Implementation Plan

- [ ] Understand requirements (brainstorming skill)
- [ ] Write tests for the fix
- [ ] Implement the fix
- [ ] Verify tests pass
- [ ] Code review

## Progress Log

- 2026-03-18: Started processing issue
- 2026-03-18: Created worktree and branch

## Skill Usage Log

| Skill | Used | Result |
|-------|------|--------|
| brainstorming | No | - |
| test-driven-development | No | - |
| subagent-driven-development | No | - |
| verification-before-completion | No | - |
| requesting-code-review | No | - |
