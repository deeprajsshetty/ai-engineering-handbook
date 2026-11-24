# 007 · LangGraph fundamentals

**Level:** Intermediate · **Prerequisites:** Module 005.

## What you learn

- Build a ReAct agent from scratch, without a framework
- Build LangGraph graphs with state, tools, memory and visualization
- Use reflection loops (tweet generator)
- Trace runs with LangSmith
- Build a multi-node essay writer with conditional edges

## Files

| Path | Contents |
|---|---|
| `01_building_an_agent_from_scratch.ipynb` | Agent loop by hand |
| `02-building-with-langgraph.ipynb` | Chatbot, tools (Tavily), memory |
| `03_reflection_tweet_generator.ipynb` | Generate, reflect, repeat |
| `04_langsmith.ipynb` | Tracing |
| `05_essay_writer.ipynb` | Project: planner, researcher, writer, critic |

## Run it

Install `requirements.txt` and set `OPENAI_API_KEY`, `TAVILY_API_KEY` and optionally `LANGSMITH_API_KEY`.

Keys go in `handbook/.env` (copy `handbook/.env.example`). Notebooks load it with `load_dotenv(find_dotenv())`. Never paste keys into cells.

## Check your understanding

Explain, for the essay writer, which edges are fixed and which ones the model decides.

## Why it matters for Sentinel

Sentinel's investigation is a LangGraph `StateGraph` with fixed edges (`sentinel/src/sentinel/agents/graph.py`).

---

← [006 Google Gemini API](../006-gemini-api/) · [Handbook index](../) · [008 Model Context Protocol (MCP) with OpenAI](../008-mcp-with-openai/) →
