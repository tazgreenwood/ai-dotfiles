# JIRA

Direct access to JIRA — look up tickets, create, transition, or comment without running the full /plan or /ticket flow.

---

## MCP CHECK

If `mcp__atlassian__*` tools are unavailable, stop immediately:

```
JIRA STATUS: MCP NOT CONFIGURED

The Atlassian MCP server is not configured. To set it up:

Option A — Remote MCP (Atlassian official, OAuth):
  claude mcp add --transport sse atlassian https://mcp.atlassian.com/v1/sse

Option B — Local MCP (API token-based):
  npm install -g @sooperset/mcp-atlassian
  Then add to Claude Code MCP settings with:
    ATLASSIAN_URL=https://clearlink.atlassian.net
    ATLASSIAN_EMAIL=<your-email>
    ATLASSIAN_TOKEN=<your-api-token>
  API tokens: https://id.atlassian.com/manage-profile/security/api-tokens
```

---

## ARG PARSING

Parse the user's input to determine the command:

| Input pattern | Command |
|---|---|
| Bare ticket key (e.g. `ONE-1234`) | **lookup** |
| `create [description]` | **create** |
| `transition [KEY] [status]` | **transition** |
| `comment [KEY] [text]` | **comment** |

If input doesn't match any pattern, print usage:

```
Usage:
  /jira ONE-1234                          — look up a ticket
  /jira create 'description'              — create a new ticket
  /jira transition ONE-1234 'In Progress' — transition ticket status
  /jira comment ONE-1234 'comment text'   — add a comment
```

---

## ROUTING

This skill is pure routing. Invoke `@jira` with the parsed command and return its output directly to the user.

### lookup

Invoke `@jira` with:
- Action: look up ticket
- Ticket key: [KEY]

Return full formatted ticket details from `@jira`.

### create

Invoke `@jira` with:
- Action: create ticket
- Description: [description from user input]
- Issue type: infer from description (Story if unclear)
- Priority: Medium (unless user specifies)
- cloudId: `13763486-d2ca-446d-9e44-3ecfc2cbb40d`
- projectKey: `ONE`
- component: `Data Platform Services Engineering`

Return the ticket key and URL from `@jira`:
```
Created: ONE-XXXX
https://clearlink.atlassian.net/browse/ONE-XXXX
```

### transition

Invoke `@jira` with:
- Action: transition ticket
- Ticket key: [KEY]
- Target status: [status]

Return the transition result from `@jira`.

### comment

Invoke `@jira` with:
- Action: add comment
- Ticket key: [KEY]
- Comment text: [text]

Return the comment confirmation from `@jira`.

## SELF-IMPROVEMENT

At the end of each run, reflect on what you learned. If anything is worth saving, act on it before returning to the user.

**Save resource discoveries** — any URL, channel ID, repo slug, log group, cluster name, or other reusable external resource found during this run:
```
registry_set(project_name, "resources.{category}.{key}", value)
```
Categories: `grafana`, `slack`, `aws`, `bitbucket`, `confluence`, `jira`, `scripts`.
Example: `registry_set("emily", "resources.grafana.api_dashboard", "http://grafana/d/abc123")`

**Save reusable commands/lookups** — any command or lookup derived this run that could be reused instead of re-derived next time:
```
registry_set(project_name, "resources.scripts.{name}", {command: "...", description: "...", learned_at: "<RFC3339 timestamp>"})
```

**Fix wrong project metadata** — if deploy.cluster, repo.base, or any other registry field was incorrect:
```
registry_set(project_name, "deploy.cluster", correct_value)
```

**Improve this skill** — if a better approach was found, make a targeted minimal edit to:
`/Users/taz.greenwood/github.com/tazgreenwood/private-dotfiles/claude/skills/jira.md`
Edit only the specific line or section that was wrong or incomplete. Do not rewrite the whole file.

Skip all of the above if nothing new was learned.
