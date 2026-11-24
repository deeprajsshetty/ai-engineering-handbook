# 001 · FastAPI and MCP basics (todo API)

**Level:** Beginner · **Prerequisites:** Module 000; HTTP basics.

## What you learn

- Build a CRUD REST API with FastAPI and Pydantic validation
- Persist to SQLite
- Expose API endpoints as MCP tools with `fastapi-mcp`
- Deploy with a Render blueprint (`render.yaml` at the repo root)

## Files

| Path | Contents |
|---|---|
| `main.py` | The API and MCP mount |
| `requirements.txt` | Pinned dependencies |

## Run it

```bash
cd handbook/001-fastapi-mcp-todo
python3 -m venv .venv && .venv/bin/pip install -r requirements.txt
.venv/bin/uvicorn main:app --reload   # http://localhost:8000/docs
```

`todos.db` is created on first run (it is git-ignored).

## Check your understanding

Add a `priority` field with validation, then call the new tool through an MCP client.

## Why it matters for Sentinel

Sentinel mounts read-only MCP tools the same way (`sentinel/src/sentinel/api/app.py`).

---

← [000 Python data foundations](../000-python-data-foundations/) · [Handbook index](../) · [002 OpenAI APIs](../002-openai-apis/) →
