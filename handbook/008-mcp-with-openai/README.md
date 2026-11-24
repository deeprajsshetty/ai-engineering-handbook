# 008 · Model Context Protocol (MCP) with OpenAI

**Level:** Intermediate · **Prerequisites:** Modules 001 and 002.

## What you learn

- Call remote MCP servers from the OpenAI Responses API
- Require approval for tool calls, globally or per tool
- Authenticate to an MCP server (Stripe example)

## Files

| Path | Contents |
|---|---|
| `openai_mcp.ipynb` | Solution |
| `openai_mcp_starter.ipynb` | Exercise starter |
| `model_context_protocol_notes.docx` | Course notes |

## Run it

These notebooks were written for **Google Colab**: they read keys with `google.colab.userdata` and install packages with `!pip`. To run them locally, replace `userdata.get("OPENAI_API_KEY")` with `os.environ["OPENAI_API_KEY"]` (loaded from `handbook/.env`), and run the `pip install` lines in your virtualenv.

## Check your understanding

Add an approval requirement for one tool, and show the run pausing until you approve.

## Why it matters for Sentinel

Sentinel exposes read-only MCP tools and keeps every approval with humans.

---

← [007 LangGraph fundamentals](../007-langgraph-fundamentals/) · [Handbook index](../) · [009 OpenAI Agents SDK](../009-openai-agents-sdk/) →
