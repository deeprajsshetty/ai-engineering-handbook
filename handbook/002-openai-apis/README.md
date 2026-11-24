# 002 · OpenAI APIs

**Level:** Beginner · **Prerequisites:** Module 000.

## What you learn

- Authenticate safely and make Chat Completions requests
- Use system and developer roles, parameters, streaming, and reasoning models
- Generate, vary and edit images (DALL-E), and use image inputs
- Transcribe and translate (Whisper), and synthesize speech (TTS)

## Files

| Path | Contents |
|---|---|
| `00_openai_apis_overview.ipynb` | One-notebook tour of everything below, including reasoning models (o1/o3). Uses the media at the module root. |
| `chat-completions/` | Auth, requests, parameters, system role, v2 SDK |
| `gpt-image-multimodal/` | Image inputs (local base64 and URLs) |
| `dall-e/` | Image generation, variations and edits |
| `whisper-and-tts/` | Speech to text and text to speech |
| `white-papers/` | Background papers: Transformer, InstructGPT, DALL-E 2/CLIP, Whisper |

## Run it

Install `chat-completions/requirements.txt` (the DALL-E lessons have their own) and set `OPENAI_API_KEY` in `handbook/.env`.

Keys go in `handbook/.env` (copy `handbook/.env.example`). Notebooks load it with `load_dotenv(find_dotenv())`. Never paste keys into cells.

## Check your understanding

Stream a completion and print tokens as they arrive. Then explain what `temperature` changed.

## Why it matters for Sentinel

Sentinel calls the same APIs over plain HTTP, with retries and fallback (`llm/providers.py`).

---

← [001 FastAPI and MCP basics (todo API)](../001-fastapi-mcp-todo/) · [Handbook index](../) · [003 Prompt engineering](../003-prompt-engineering/) →
