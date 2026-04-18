# Model Intelligence

clawd picks the right model for each task automatically. It analyzes your message before sending it to an AI provider and selects a model tier based on what the task actually needs. You keep full control: pin a model for the whole session, set a monthly spend cap, or disable auto-select entirely.

---

## Overview

Every message you send goes through a three-stage pipeline before it reaches the AI:

1. **Classify** — A fast heuristic (no LLM call, under 1ms) reads your message and scores it on signals like word count, file references, code blocks, and keywords. The score maps to one of four complexity levels: Simple, Moderate, Complex, or DeepReasoning.

2. **Select** — The complexity level maps to a model tier. Simple tasks go to Haiku-class models. Moderate and Complex tasks use Sonnet. DeepReasoning tasks use Opus. Your config and session pin can override this.

3. **Evaluate** — After the response comes back, clawd checks if it looks like a refusal, an empty response, or a tool call error. If so, it retries once with the next model tier. Maximum one upgrade per message.

Token counts and estimated costs are stored after each response so you can see exactly what each session costs.

---

## How Auto-Select Works

The classifier fires signals based on what it reads in your message. Each signal adjusts a score up or down. Higher scores route to more capable (and more expensive) models.

**Signals that push toward simpler models:**
- Short message (under 20 words): -2 points
- Simple keywords like "rename", "fix typo", "what is", "explain this line": -2 points

**Signals that push toward more capable models:**
- Long message (over 50 words): +2 points; over 200 words: +4 points
- File references (`.rs`, `.ts`, `/path/to/file`): +1 point each, up to +3
- Code blocks: +1 per pair, up to +3
- Moderate keywords like "refactor", "implement", "write a function": +2 points
- Complex keywords like "audit", "security", "authentication", "across the codebase": +4 points each
- Deep keywords like "architect from scratch", "novel", "full audit": forces DeepReasoning regardless of other signals
- Long session history (over 20 messages): +2 points

**Score ranges:**

| Score | Complexity | Model tier |
|-------|------------|------------|
| 0-2 | Simple | Haiku |
| 3-5 | Moderate | Sonnet |
| 6-9 | Complex | Sonnet |
| 10+ | DeepReasoning | Opus |

If the model returns a refusal or empty response, clawd upgrades one tier and retries. This upgrade happens at most once per message to keep costs predictable.

---

## Choosing a Model Manually

To lock a specific model for the session, tap the model chip in the session header. A bottom sheet opens with four choices: Auto (daemon routes per message), Haiku, Sonnet, or Opus. The chip turns amber when a model is pinned.

The session list shows a small amber indicator on each session that has a pinned model, so you can see at a glance which sessions are on auto-routing and which are pinned.

To disable auto-select entirely in config (always use the same model regardless of task), set `auto_select = false`. clawd will use the `complexity_floor` model for every message.

---

## Repo Context Registry

Each session can carry a prioritized list of repo paths that get injected into the context before the message is sent. Lower-priority entries are evicted first when the context window is tight.

**Add a path:**

```text
session.addRepoContext { sessionId, path, priority }   # priority 1–10 (default 5)
```

**List paths:**

```text
session.listRepoContexts { sessionId }
```

**Remove a path:**

```text
session.removeRepoContext { id }
```

Context entries are stored in SQLite under the `session_contexts` table and persist across daemon restarts.

---

## Token Tracking

clawd stores input tokens, output tokens, and estimated cost after every AI response. You can view this breakdown in the token usage panel in the session UI.

**Per-session view:** Shows total tokens and cost for the current session.

**Monthly breakdown:** Groups usage by model across the current calendar month, ordered by cost. The most expensive model appears first.

Cost estimates use a static pricing table updated when providers change their rates. Unknown future models show $0 until the table is updated — the token counts still record correctly.

---

## Budget Controls

Add a `[model_intelligence]` section to your `~/.config/clawd/config.toml`:

```toml
[model_intelligence]
# Disable auto-select — always use the complexity_floor model
auto_select = true

# Never route tasks below this complexity (even if classified as Simple)
# Values: "Simple", "Moderate", "Complex", "DeepReasoning"
complexity_floor = "Simple"

# Never use a model above this tier (auto-select respects this cap)
# Values: "haiku", "sonnet", "opus"
max_model = "opus"

# Monthly spend cap in USD. 0 = no cap.
# Warning at 80%, forced downgrade to Haiku at 100%.
monthly_budget_usd = 10.00
```

When you hit 80% of your monthly cap, clawd fires a budget warning event to the client. At 100%, all tasks route to Haiku until the month rolls over.

---

## FAQ

**Why did my simple question get routed to Sonnet instead of Haiku?**
The classifier works on signals — a short question with a file reference and a code block scores higher than a short question with no context. You can see exactly which signals fired in the debug logs (`RUST_LOG=clawd=debug`). Pinning to Haiku in the session header bypasses this for the current session.

**Does the classifier make an LLM call to classify my message?**
No. It is a fast heuristic — regex matching and word counting. It runs in under 1ms and never sends your message to an AI provider before the main call.

**Can I pin a specific model version (like claude-sonnet-4-6-20251015)?**
Yes. Any model ID string works as a pin. Unknown models fall back to the claude provider and record $0 cost until the pricing table is updated.

**What counts as a "poor" response that triggers an upgrade?**
An empty response, a model refusal ("I'm unable to...", "as an AI..."), a tool call error, or a response flagged as truncated. Good responses never trigger an upgrade.

**How do I reset my monthly budget counter?**
Budget tracking is per calendar month. It resets automatically on the 1st of each month. You cannot manually reset it mid-month.

**Does auto-upgrade cost extra?**
Yes — an upgrade means two AI calls instead of one for that message. The cost of both calls is recorded and counts toward your monthly budget.

---

## See Also

- [Session Manager](Session-Manager) — sessions, message flow, tool calls
- [Home](Home)
