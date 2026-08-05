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
