# private-dotfiles

Personal dev environment config. Private.

## Setup

```bash
git clone git@github.com:tazgreenwood/private-dotfiles.git ~/github.com/tazgreenwood/private-dotfiles
cd ~/github.com/tazgreenwood/private-dotfiles
chmod +x install.sh && ./install.sh
```

Update: `git pull && ./install.sh`

---

## Structure

```
claude/
  skills/         → ~/.claude/skills/{name}/SKILL.md (symlinked)
  agents/         → ~/.claude/agents/{name}.md (symlinked)
  mcp/
    server/       → registry MCP server (Node)
    data/         → project registry (gitignored — local state)
zsh/              → zsh config
nvim/             → neovim config
```

---

## Claude Skills

### Workflow (run in sequence for feature development)
| Skill | Description |
|-------|-------------|
| `/ticket` | Triage issue, create Jira ticket |
| `/plan` | Research ticket, write step-by-step plan |
| `/build` | Execute one plan step at a time |
| `/pr-review` | Review PR feedback, iterate |
| `/ship` | Final checks, merge, audit trail |

### Utilities (standalone)
| Skill | Description |
|-------|-------------|
| `/deploy-check` | ECS health + CloudWatch error scan, before/after deploy diff |
| `/research` | Deep multi-source research on any topic |
| `/dps` | DPS orchestrator (legacy, being phased out) |

---

## Claude Agents

Agents are invoked by skills — not called directly.

| Agent | Purpose |
|-------|---------|
| `planner` | Writes step-by-step plan JSON |
| `developer` | Implements one plan step |
| `qa` | Runs tests, signs off GO/NO-GO |
| `reviewer` | Code review, finds reasons to reject |
| `security` | OWASP audit for high-risk changes |
| `handover` | PR description + audit trail |
| `investigator` | Root cause analysis, read-only |
| `jira` | Jira ticket operations |
| `confluence` | Confluence page operations |
| `triage` | Intake unstructured bug reports |
| `designer` | UX audit before implementation |
| `documenter` | Keeps docs in sync after changes |
| `init` | Scans repo, writes CLAUDE.md |

---

## Registry MCP

Local MCP server for project metadata. Auto-configured by `install.sh`.

Tools: `get_project`, `set`, `init_project`, `list_plans`, `get_plan`, `write_audit`

Data lives in `claude/mcp/data/` — gitignored (local state, not shared).
