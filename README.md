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
| `/plan` | Auto-generate ticket key (or use JIRA); write step-by-step plan with AC |
| `/build` | Execute one plan step at a time |
| `/ship` | Security audit, code review, create PR, write audit trail to registry |

### Direct tools (standalone)
| Skill | Description |
|-------|-------------|
| `/jira` | Look up, create, transition, or comment on JIRA tickets |
| `/confluence` | Search, create, or update Confluence pages |
| `/standup` | Synthesize Yesterday/Today/Blockers from JIRA + Slack; post to channel |
| `/shipped` | Monthly/quarterly work history from audit trail (perf review, reconciliation) |

---

## Claude Agents

Agents are invoked by skills — not called directly.

| Agent | Purpose |
|-------|---------|
| `jira` | JIRA ticket operations (lookup, create, transition, comment) |
| `confluence` | Confluence page operations (search, create, update) |
| `standup` | Synthesize work items from JIRA + Slack, post to channel |
| `reviewer` | Code review, finds reasons to reject |
| `security` | OWASP audit for high-risk changes |
| `documenter` | Syncs CLAUDE.md, decisions, glossary, external docs |
| `handover` | Creates PR description and hands off to Bitbucket |

---

## Registry MCP

Local MCP server for project metadata and audit trails. Built in Go; auto-configured by `install.sh`.

**Project metadata**: `registry_get_project`, `registry_set`, `registry_init_project`, `registry_list_projects`

**Plans**: `registry_list_plans`, `registry_get_plan`, `registry_write_plan`

**Audit trail**: `registry_write_audit`, `registry_get_audit` (with date range filtering)

**Bitbucket integration**: `bitbucket_list_prs`, `bitbucket_get_pr`, `bitbucket_create_pr`, `bitbucket_get_commits`, `bitbucket_add_pr_comment`, `bitbucket_get_repo`, `bitbucket_list_branches`, `bitbucket_get_diff`

Data lives in `~/.config/registry/data/` — local state, not shared in git.
