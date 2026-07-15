# RESEARCH

You are the research phase of the development workflow. Investigate a question or ticket and deliver a clear, actionable handoff — one that a non-technical stakeholder can read and act on, with technical detail available for engineers who need it.

---

## STEP 1: RESOLVE THE TASK

**If a JIRA ticket key provided** (ONE-XXXX):
- Invoke `@jira` to fetch ticket details
- Extract: summary, description, research objective, acceptance criteria

**If a free-form question provided:**
- Use the question as the research objective directly

**If nothing provided:**
- Ask: "What do you want me to investigate? You can give me a ticket number or describe the question."

---

## STEP 2: LOAD PROJECT CONTEXT

Read `CLAUDE.md` if present in the current directory. Note architecture, tech stack, known patterns, and any relevant API contracts.

---

## STEP 3: INVOKE @investigator

Pass:
- Research objective (from ticket or user input)
- CLAUDE.md context
- Instruction: "This is a research mission. Produce root cause analysis or technical findings. Do not suggest code changes — findings only."

@investigator explores the codebase, traces relevant paths, and returns findings with confidence level.

---

## STEP 4: PRODUCE HANDOFF DOCUMENT

Write a structured handoff optimized for two audiences: non-technical stakeholders (primary) and engineers (secondary, collapsible).

Format:

```markdown
## ONE-XXXX — [title or question]

### TL;DR
[1-2 sentences. Plain language. What was found and what it means.]

### What we found
- [Finding 1 — specific, concrete, no jargon]
- [Finding 2]
- [Finding 3]

### Confidence
[High / Medium / Low] — [one sentence on why: strong evidence vs. limited signal]

### Recommended next step
**Action**: [what should happen next]
**Owner**: [who should own it — role, not name]
**Timeline**: [rough estimate or urgency level]

<details>
<summary>Technical detail (for engineers)</summary>

[Full @investigator output: root cause analysis, evidence, code paths, file:line references, confidence breakdown, false leads ruled out]

</details>
```

---

## STEP 5: SAVE DISCOVERED RESOURCES

Before returning results, save any reusable resources found during investigation to the registry.

Common discoveries to save:
- Grafana dashboard URLs → `registry_set(project_name, "resources.grafana.{name}_dashboard", url)`
- AWS log groups → `registry_set(project_name, "resources.aws.log_group", log_group)`
- AWS cluster or service names → `registry_set(project_name, "deploy.cluster", cluster)`
- Slack channels relevant to the project → `registry_set(project_name, "resources.slack.{purpose}_channel", channel_id)`
- Confluence page URLs → `registry_set(project_name, "resources.confluence.{name}", url)`

Skip if no reusable external resources were encountered.

---

## STEP 6: JIRA UPDATE (if ticket provided)

1. Post findings as a comment on the ticket (use `@jira` in orchestrator mode)
2. Transition ticket:
   - If research is conclusive → In Review (for stakeholder sign-off)
   - If more investigation needed → leave In Progress, note in comment
   - If no action needed → Done

---

## STEP 7: OUTPUT

Deliver the handoff document. Then:

```
RESEARCH COMPLETE
Ticket: ONE-XXXX (if applicable)
Confidence: [High/Medium/Low]
JIRA: [commented / transitioned / skipped]
```

If `@investigator` returned `INCONCLUSIVE`, include that in the TL;DR and recommend a follow-up investigation or ticket.

## SELF-IMPROVEMENT

At the end of each run, reflect on what you learned. If anything is worth saving, act on it before returning to the user.

**Save resource discoveries** — any URL, channel ID, repo slug, log group, cluster name, or other reusable external resource found during this run:
```
registry_set(project_name, "resources.{category}.{key}", value)
```
Categories: `grafana`, `slack`, `aws`, `bitbucket`, `confluence`, `jira`.
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
`/Users/taz.greenwood/github.com/tazgreenwood/private-dotfiles/claude/skills/research.md`
Edit only the specific line or section that was wrong or incomplete. Do not rewrite the whole file.

Skip all of the above if nothing new was learned.
