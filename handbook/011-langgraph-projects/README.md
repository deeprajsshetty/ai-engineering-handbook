# 011 · LangGraph projects

**Level:** Advanced · **Prerequisites:** Modules 007 and 010.

## What you learn

- Use routers and conditional workflows in LangGraph
- Port the CrewAI interview coach (module 010) to a LangGraph workflow

## Files

| Path | Contents |
|---|---|
| `01-joke-bot/` | Router-driven conversational bot (solution, starter, graph image) |
| `02-interview-coach-part-3/` | Interview coach as a graph: research, questions, routing |

## Run it

These notebooks were written for **Google Colab**: they read keys with `google.colab.userdata` and install packages with `!pip`. To run them locally, replace `userdata.get("OPENAI_API_KEY")` with `os.environ["OPENAI_API_KEY"]` (loaded from `handbook/.env`), and run the `pip install` lines in your virtualenv. Supply your own or fictional interview details (see module 010).

## Check your understanding

Draw the interview-coach graph and mark which decisions are code and which are the model's.

## Why it matters for Sentinel

This is the same workflow-versus-agent trade-off discussed in `docs/guide/agentic-engineering-guide.md` §4.6.

---

← [010 CrewAI multi-agent systems](../010-crewai-multi-agent/) · [Handbook index](../) · [012 Capstone: research agent](../012-capstone-research-agent/) →
