# 010 · CrewAI multi-agent systems

**Level:** Advanced · **Prerequisites:** Module 007.

## What you learn

- Define agents, tasks and crews
- Run research crews with web tools
- Automate document workflows (PDF invoices to CSV, then answer queries)
- Score leads with a research agent plus a scoring agent (capstone)

## Files

| Path | Contents |
|---|---|
| `01-job-interview-prep-part-1/` | Interviewer agent and coach agent |
| `02-job-interview-prep-part-2/` | Research crew and coaching crew (`_alternate` is a second solution) |
| `03-business-automation-pdf/` | Invoice extraction crew; sample `invoices/` |
| `04-capstone-partnership-lead-scoring/` | Capstone: solution, starter and project briefing |

## Run it

These notebooks were written for **Google Colab**: they read keys with `google.colab.userdata` and install packages with `!pip`. To run them locally, replace `userdata.get("OPENAI_API_KEY")` with `os.environ["OPENAI_API_KEY"]` (loaded from `handbook/.env`), and run the `pip install` lines in your virtualenv.

**Inputs:** the interview-prep lessons ask for an interviewer name, a company, and a job description. Use your own or fictional details. The original inputs and generated reports profiled real people, so they were moved to `_archive/` (see `_archive/MANIFEST.md`).

## Check your understanding

Explain when a crew of agents beats one agent with several tools, and when it does not.

## Why it matters for Sentinel

Multi-agent orchestration has a cost. Sentinel deliberately uses a deterministic pipeline instead.

---

← [009 OpenAI Agents SDK](../009-openai-agents-sdk/) · [Handbook index](../) · [011 LangGraph projects](../011-langgraph-projects/) →
