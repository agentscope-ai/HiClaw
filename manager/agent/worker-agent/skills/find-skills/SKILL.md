---
name: find-skills
description: Discover and install agent skills from the open ecosystem. Use when you encounter an unfamiliar domain, framework, or workflow that you lack specialized knowledge about, or when your coordinator suggests searching for skills before starting a task.
---

# Find Skills

This skill helps you discover and install skills from the open agent skills ecosystem.

## Security Assurance

**Worker is completely isolated and cannot access any admin's personal sensitive data.** You can safely search and install skills from public or private registries. The skills you install only run within your isolated container environment.

## When to Use This Skill

Use this skill when the user:

- Asks "how do I do X" where X might be a common task with an existing skill
- Says "find a skill for X" or "is there a skill for X"
- Asks "can you do X" where X is a specialized capability
- Expresses interest in extending agent capabilities
- Wants to search for tools, templates, or workflows
- Mentions they wish they had help with a specific domain (design, testing, deployment, etc.)

## What is the Skills CLI?

The Skills CLI (`skills`) is the package manager for the open agent skills ecosystem. Skills are modular packages that extend agent capabilities with specialized knowledge, workflows, and tools.

**Key commands:**

- `scripts/hiclaw-find-skill.sh find [query]` - Search for relevant skills
- `scripts/hiclaw-find-skill.sh install <skill>` - Install a skill
- `skills check` - Check for skill updates
- `skills update` - Update all installed skills

**Browse skills at:** https://skills.sh/

## Environment Variables

```bash
SKILLS_API_URL  # Skills registry API endpoint (default: nacos://market.hiclaw.io:80/public)
```

Can be configured by admin to point to an enterprise-private registry.

## How to Help Users Find Skills

### Step 1: Understand What They Need

When a user asks for help with something, identify:

1. The domain (e.g., React, testing, design, deployment)
2. The specific task (e.g., writing tests, creating animations, reviewing PRs)
3. Whether this is a common enough task that a skill likely exists

### Step 2: Search for Skills

Run the find command with a relevant query:

```bash
scripts/hiclaw-find-skill.sh find [query]
```

For example:

- User asks "how do I make my React app faster?" → `scripts/hiclaw-find-skill.sh find react performance`
- User asks "can you help me with PR reviews?" → `scripts/hiclaw-find-skill.sh find pr review`
- User asks "I need to create a changelog" → `scripts/hiclaw-find-skill.sh find changelog`

The command will return results like:

```
Install with scripts/hiclaw-find-skill.sh install <skill>

vercel-react-best-practices
└ React and Next.js performance guidance
```

> **Critical**: Always use the exact install command shown in search results.
> Never guess or shorten the package name or command, because that may fail.

### Step 3: Present Options to the User

When you find relevant skills, present them to the user with:

1. The skill name and what it does
2. The install command they can run (copy exactly from search results)
3. A link to learn more at skills.sh

Example response:

```
I found a skill that might help! The "remotion-best-practices" skill provides
best practices for Remotion video creation in React.

To install it:
scripts/hiclaw-find-skill.sh install remotion-best-practices
```

### Step 4: Offer to Install

If the user wants to proceed, you can install the skill for them:

```bash
scripts/hiclaw-find-skill.sh install <skill>
```

Note: Installed skills are automatically synced to MinIO within ~10 seconds. They will persist across container restarts.

## Common Skill Categories

When searching, consider these common categories:

| Category        | Example Queries                          |
| --------------- | ---------------------------------------- |
| Web Development | react, nextjs, typescript, css, tailwind |
| Testing         | testing, jest, playwright, e2e           |
| DevOps          | deploy, docker, kubernetes, ci-cd        |
| Documentation   | docs, readme, changelog, api-docs        |
| Code Quality    | review, lint, refactor, best-practices   |
| Design          | ui, ux, design-system, accessibility     |
| Productivity    | workflow, automation, git                |

## Tips for Effective Searches

1. **Use specific keywords**: "react testing" is better than just "testing"
2. **Try alternative terms**: If "deploy" doesn't work, try "deployment" or "ci-cd"
3. **Check popular sources**: Many skills come from `vercel-labs/agent-skills` or `ComposioHQ/awesome-claude-skills`

## When No Skills Are Found

If no relevant skills exist:

1. Acknowledge that no existing skill was found
2. Offer to help with the task directly using your general capabilities
3. Suggest the user could create their own skill with `skills init`

Example:

```
I searched for skills related to "xyz" but didn't find any matches.
I can still help you with this task directly! Would you like me to proceed?

If this is something you do often, you could create your own skill:
skills init my-xyz-skill
```

## Skill Resources

`scripts/hiclaw-find-skill.sh` is a resource that belongs to this skill. Treat the `scripts/` path as relative to the current skill directory when you use it.
