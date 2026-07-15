# CONFLUENCE

Direct access to Confluence — search pages, create documentation, or update existing pages without running the full /ship documenter flow.

---

## MCP CHECK

If `mcp__atlassian__*` tools are unavailable, stop immediately:

```
CONFLUENCE STATUS: MCP NOT CONFIGURED

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
| `search [query]` | **search** |
| `create [topic]` | **create** (under DPS Projects) |
| `create [topic] [parent]` | **create** (under specified parent) |
| `update [page title or ID]` | **update** |

If input doesn't match any pattern, print usage:

```
Usage:
  /confluence search 'emily architecture'  — search for matching pages
  /confluence create 'New Runbook'         — create page under DPS Projects
  /confluence create 'Runbook' 'My App'    — create page under specified parent
  /confluence update 'Page Title'          — update an existing page
  /confluence update 4476436483            — update page by ID
```

---

## ROUTING

This skill is pure routing. Invoke `@confluence` with the parsed command and return its output directly to the user.

### search

Invoke `@confluence` with:
- Action: search for pages
- Query: [query from user input]

Return the matching pages with titles, URLs, and last-modified dates from `@confluence`.

### create

Invoke `@confluence` with:
- Action: create a page
- Topic/title: [topic from user input]
- Parent: if user specified a parent, pass it; otherwise default to DPS Projects (page ID `4476436483`)
- Space: `DPS`

Return the result from `@confluence`:
```
CONFLUENCE STATUS: CREATED — [Page Title] — [URL]
Page ID: [id]
```

### update

Invoke `@confluence` with:
- Action: update an existing page
- Page title or ID: [value from user input]

Return the result from `@confluence`:
```
CONFLUENCE STATUS: UPDATED — [Page Title] — [URL]
```

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
`/Users/taz.greenwood/github.com/tazgreenwood/private-dotfiles/claude/skills/confluence.md`
Edit only the specific line or section that was wrong or incomplete. Do not rewrite the whole file.

Skip all of the above if nothing new was learned.
