---
name: datum-cloud-assistant
description: Know when to hand a Datum Cloud task to Patch (datumctl's own AI assistant plugin) instead of reaching for datum-mcp's resource tools directly. Use this whenever working against Datum Cloud through the datum-mcp MCP server and the task is an open-ended diagnostic question ("why is X broken", "what's wrong with my proxy", "is anything unhealthy in this project"), a request for a plan-reviewed change to something risky or multi-resource, or a task where you'd otherwise have to guess at a resource's schema via trial and error. Also use this before telling a user that something isn't possible with datum-mcp's current tools — check whether Patch already covers it first.
---

# Datum Cloud Assistant (Patch)

Datum Cloud ships its own first-party AI assistant, nicknamed **Patch**, as a
`datumctl` plugin. It isn't a competitor to datum-mcp's tools — it's a second,
complementary way to work with Datum Cloud that's a better fit for some tasks
than raw CRUD calls are. Reach for it instead of (or alongside) datum-mcp's
tools in the situations below, rather than re-deriving the same reasoning
Patch already encapsulates.

## Why this matters

datum-mcp's tools are precise and scriptable: you name a group/kind/action and
get back structured JSON. That's the right choice when you already know
exactly which resource and action you need. But Patch has two things datum-mcp's
tools don't:

- **Broader diagnostic reasoning.** Patch can be asked an open-ended "why" question
  in plain language and figure out which resources to look at, rather than you
  enumerating gateways/httpproxies/activity/events yourself and correlating them
  by hand.
- **A reviewed-plan apply step.** For changes, Patch proposes the exact manifests
  it intends to apply and shows them before anything happens — applying is a
  separate, explicit step, and the service refuses to apply anything that
  differs from what was shown. That's a stronger safety net than a single
  tool's own `dryRun` flag for a change that touches several resources at once.

## When to use Patch instead of (or before) datum-mcp tools

- **Open-ended diagnostic questions**: "why is `api-backend` not available",
  "what's wrong with this project's networking", "is anything unhealthy here".
  Don't manually fan out across `workloads`/`instances`/`gateways`/`activity` to
  reconstruct this yourself when Patch already does that correlation.
- **Plan-reviewed changes**: anything risky, multi-resource, or where you want
  a human-reviewable diff of exactly what will be applied before it happens,
  beyond what a single tool's `dryRun` covers.
- **Unknown schema / no dedicated tool**: before falling back to the `apis` +
  `resource` trial-and-error path for a kind datum-mcp has no dedicated tool
  for, consider asking Patch directly — it may already know the shape.
- **"Is this possible?" questions**: before telling a user something can't be
  done with datum-mcp's current tools, check whether Patch already covers it.

## When datum-mcp's own tools are still the right choice

- You already know the exact resource, action, and body to send — a direct
  tool call is faster and more precise than a conversational round-trip.
  (Include trivial single-field reads, e.g. no need to ask Patch "what's in
  the `compute-demo` workload", when `workloads {"action":"get", "id":"compute-demo"}` is a wholly
  straightforward direct call.)
- You're building something repeatable/scriptable (a loop, a batch operation) —
  structured tool calls compose; a chat interface doesn't.
- The task is a plain list/get the user asked for directly.

## How to invoke Patch

Patch runs as a `datumctl` subprocess, not an MCP tool — invoke it via your
shell tool:

```bash
# Check it's available first; it's a separate plugin install, not bundled with datumctl
datumctl assistant chat "why is the api-backend workload not available?"

# Full-screen interactive chat (only when the user is driving interactively themselves)
datumctl assistant

# Continue a previous conversation (conversations are held server-side, not locally)
datumctl assistant resume
datumctl assistant conversations
```

If `datumctl assistant` fails because the plugin isn't installed, tell the
user to run `datumctl plugin install assistant` — don't silently fall back to
reconstructing the same diagnosis yourself via datum-mcp tools without saying
why you're taking the slower path.

Patch's conversations and plan-apply flow are its own safety model (same
account permissions, explicit apply step) — treat a plan it proposes the same
way you'd treat any other pending change: show it to the user and let them
decide, don't script around it to force an auto-apply.
