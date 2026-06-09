# SHIPPED

Work history report for perf reviews, monthly reconciliation, and quarterly reporting.

---

## MCP CHECK

Check availability before proceeding:

- `mcp__clearlink-registry__*` — needed for audit trail queries
- `mcp__atlassian__*` — needed for JIRA supplement

If registry MCP is unavailable: warn `SHIPPED WARNING: Registry MCP not configured — audit trail will be skipped.` Continue with JIRA only.

If Atlassian MCP is unavailable: warn `SHIPPED WARNING: Atlassian MCP not configured — JIRA supplement will be skipped.`

If both are unavailable, stop:

```
SHIPPED STATUS: NO DATA SOURCES

Neither Registry nor Atlassian MCPs are configured. At least one is required.
```

---

## ARG PARSING

Parse the following flags from the user's input (or the slash command arguments):

- `--month YYYY-MM` — filter to that calendar month (e.g. `--month 2026-05`)
- `--quarter YYYY-QN` — filter to that quarter (Q1=Jan-Mar, Q2=Apr-Jun, Q3=Jul-Sep, Q4=Oct-Dec)
- `--year YYYY` — filter to the full calendar year
- `--narrative` — switch to prose output mode (default: list mode)

**No args = current month, list mode.**

Only one of `--month`, `--quarter`, `--year` may be specified. If multiple are provided, use the first one encountered.

---

## STEP 1: RESOLVE DATE RANGE

Convert the parsed arg to `since` and `until` ISO dates (YYYY-MM-DD):

- `--month YYYY-MM`: since = `YYYY-MM-01`, until = last day of that month
- `--quarter YYYY-QN`:
  - Q1: since = `YYYY-01-01`, until = `YYYY-03-31`
  - Q2: since = `YYYY-04-01`, until = `YYYY-06-30`
  - Q3: since = `YYYY-07-01`, until = `YYYY-09-30`
  - Q4: since = `YYYY-10-01`, until = `YYYY-12-31`
- `--year YYYY`: since = `YYYY-01-01`, until = `YYYY-12-31`
- No arg: since = first day of current month, until = today

---

## STEP 2: QUERY REGISTRY

Detect the current project name from the git remote (strip workspace prefix and `.git` suffix from `git remote get-url origin`).

Call `registry_get_audit` with:
- `name`: detected project name
- `since`: resolved since date
- `until`: resolved until date

Collect returned entries. Note each entry's `ticket`, `action`, `date` (or `_recorded_at`), and any PR/summary fields.

---

## STEP 3: SUPPLEMENT FROM JIRA

Query JIRA for tickets resolved in the date range:

- cloudId: `13763486-d2ca-446d-9e44-3ecfc2cbb40d`
- JQL: `assignee = currentUser() AND resolutiondate >= "since" AND resolutiondate <= "until" ORDER BY resolutiondate DESC`

Replace `since` and `until` with the resolved ISO dates.

Collect: ticket key, summary, resolution date, labels, issue type.

**Deduplicate:** remove any JIRA tickets whose key already appears in the registry audit entries. Merge the two lists into a single working set.

Each item in the working set should have: `ticket`, `summary`, `date`, `type`, `labels`, `pr` (if available from audit).

---

## STEP 4: FORMAT OUTPUT

### List mode (default)

Group items by ISO week (Monday–Sunday). For each week, print a header and then each item:

```
Week of Mon DD MMM
  [ONE-XXXX] Summary — https://bitbucket.org/.../pull-requests/N (type)
  [ONE-XXXX] Summary (type)
```

Items without a PR link omit the link portion. Sort weeks in ascending order. Sort items within each week by date ascending.

Print a summary line at the end:

```
Total: N items — YYYY-MM-DD to YYYY-MM-DD
```

### Narrative mode (`--narrative`)

Group items by theme. Infer theme from JIRA labels (use the first non-generic label), or if no useful labels exist, infer from keywords in the ticket summary (e.g. "performance", "API", "UI", "infra", "bug", "tooling").

For each theme group, write a 2–3 sentence prose paragraph suitable for copy-pasting into a perf review. Focus on impact and scope rather than implementation details. Format as markdown with a bold theme heading:

```markdown
**Theme Name**

Paragraph describing the work done in this theme, what was delivered, and its impact. Further sentences as needed. Up to 3 sentences total.
```

End with a one-line summary:

```
N items across M themes — YYYY-MM-DD to YYYY-MM-DD
```

---

## STEP 5: PRINT RESULT

Print the formatted output to the terminal. Do not post anywhere automatically.
