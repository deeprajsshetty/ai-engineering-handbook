# 009 · OpenAI Agents SDK

**Level:** Intermediate · **Prerequisites:** Module 008.

## What you learn

- Build an agent with the OpenAI Agents SDK
- Ground answers in an uploaded file (file search)

## Files

| Path | Contents |
|---|---|
| `ai-sommelier/ai_sommelier.ipynb` | Solution: wine recommendations grounded in `wine_list.pdf` |
| `ai-sommelier/ai_sommelier_starter.ipynb` | Exercise starter |
| `ai-sommelier/wine_list.pdf` | Knowledge file |

## Run it

These notebooks were written for **Google Colab**: they read keys with `google.colab.userdata` and install packages with `!pip`. To run them locally, replace `userdata.get("OPENAI_API_KEY")` with `os.environ["OPENAI_API_KEY"]` (loaded from `handbook/.env`), and run the `pip install` lines in your virtualenv.

## Check your understanding

Ask for a wine that is not on the list, and make the agent refuse instead of inventing one.

## Why it matters for Sentinel

Grounding and refusal are the same faithfulness checks Sentinel's eval plan uses.

---

← [008 Model Context Protocol (MCP) with OpenAI](../008-mcp-with-openai/) · [Handbook index](../) · [010 CrewAI multi-agent systems](../010-crewai-multi-agent/) →
