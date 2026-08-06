# IDEA VALIDATION

You are the idea-validation skill. Take a raw, freeform idea and validate it via a fan-out of isolated research agents, then synthesize the findings into a market-gap report.

---

## STEP 1: GATHER INPUT

Accept one freeform argument: the raw idea text.

```
idea-validation "<raw idea text>"
```

If no argument is provided, ask the user for the idea.

---

## STEP 2: INVOKE idea-fleshing AGENT

Invoke an isolated, general-purpose Agent call to expand the raw idea text into structured form.

Pass:
- The raw idea text from STEP 1
- Instruction: "Expand this raw idea into: a problem statement, the target user, the value proposition, and a list of hypotheses this idea rests on. Return only these four sections. Do not research competitors, market data, or feasibility — that happens separately."

This agent sees nothing except the raw idea text. Its output (problem statement, target user, value prop, hypothesis list) is the shared input handed to each STEP 3 fan-out node below.

---

## STEP 3: PARALLEL FAN-OUT — INDEPENDENT RESEARCH

In a single message, fire the following Agent calls in parallel. Each call is isolated: none sees the raw idea text beyond the STEP 2 output, and none sees any other fan-out node's output or even that other nodes exist.

### competitor-research

Tools: WebSearch, WebFetch.

Pass: the STEP 2 output (problem statement, target user, value prop, hypotheses).

Instruction: "Research existing solutions and competitors for this idea. Return: a list of existing/competing products or services, and a positioning assessment (how this idea would differentiate, if at all)."

### market-trend

Tools: WebSearch.

Pass: the STEP 2 output.

Instruction: "Research market size and trend signals relevant to this idea. Return: market size estimate (if discoverable), growth signals, and any supporting evidence found."

### risk-assumption

Tools: general-purpose (no external tools required).

Pass: the STEP 2 output.

Instruction: "Identify the key risks and unproven assumptions underlying this idea. Return: a list of key risks, and a list of assumptions that are unproven and would need validation."

### technical-feasibility

Tools: general-purpose (no external tools required).

Pass: the STEP 2 output.

Instruction: "Assess the rough build complexity of this idea given the stated problem statement and value proposition. Return: a rough complexity estimate (low/medium/high) and the reasoning behind it."

Each of the four nodes above runs with zero visibility into the other three nodes' prompts or outputs.

---

## STEP 4: SYNTHESIS

Invoke a single Agent call to synthesize the STEP 2 output and all four STEP 3 outputs into a market-gap report.

Pass:
- The STEP 2 output (problem statement, target user, value prop, hypotheses)
- The STEP 3 `competitor-research` output
- The STEP 3 `market-trend` output
- The STEP 3 `risk-assumption` output
- The STEP 3 `technical-feasibility` output

Instruction: "Given the idea summary and the four independent research findings above, produce a market-gap report with exactly these five sections:
1. **Market Gap Analysis** — where this idea sits relative to existing solutions (from competitor-research) and market signals (from market-trend); does a real gap exist
2. **Recommendation** — build / skip / pivot, and why, weighing the research findings against the risks and assumptions (from risk-assumption)
3. **MVP Scope** — if pursued, the smallest version worth building, informed by the build complexity assessment (from technical-feasibility)
4. **Key Risks** — the risks and unproven assumptions that most threaten this idea (from risk-assumption)
5. **Confidence Level** — low/medium/high confidence in the recommendation, and why

Return only these five sections."

This is the only agent call in the skill that sees more than one upstream output — it is the synthesis node, invoked after all STEP 3 nodes complete.

---

## STEP 5: PRINT REPORT

### Assemble the report data

Gather:
- **Idea summary** — the STEP 2 output (problem statement, target user, value prop, hypotheses)
- **Market Gap Analysis**, **Recommendation**, **MVP Scope**, **Key Risks**, **Confidence Level** — the five sections from STEP 4

### Default: print Markdown to chat

Print the report directly in the response as Markdown — a heading, then one `##` section per report part: Idea Summary, Market Gap Analysis, Recommendation, MVP Scope, Key Risks, Confidence Level. This is the default output; no file is written.

### On request: render a shareable HTML file

Only if the user asks to save or share the report (e.g. "save this", "give me a file"), render a single self-contained HTML file (inline `<style>`, no external assets), mirroring the section layout established by `claude/ui/templates/review.html`.

Write it to a temp path — note `mktemp -t` requires the `X`s to be the trailing characters of the template, so generate the random name first and append the extension:
```bash
f="$(mktemp -t idea-validation).html"
```

Populate the file's sections with the assembled data (escape HTML-sensitive characters), then open it:
```bash
open "$f"
```

If `open` is unavailable (non-macOS), print the file path to the user instead.

---

## STEP 6: CONFIRM

```
IDEA VALIDATION COMPLETE
Idea: [one-line restatement of the raw idea]
Recommendation: [build / skip / pivot]
Confidence: [low / medium / high]
Report: [path to HTML file] (opened in browser)
```

No tickets or plans are auto-created from this report. The human decides the next step.

---

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
`/Users/taz.greenwood/github.com/tazgreenwood/private-dotfiles/claude/skills/idea-validation.md`
Edit only the specific line or section that was wrong or incomplete. Do not rewrite the whole file.

Skip all of the above if nothing new was learned.
