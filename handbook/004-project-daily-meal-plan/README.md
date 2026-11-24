# 004 · Project: AI daily meal plan

**Level:** Beginner project · **Prerequisites:** Modules 002 and 003.

## What you learn

- Chain text generation, image generation and TTS into one pipeline

## Files

| Path | Contents |
|---|---|
| `daily_meal_plan.ipynb` | The pipeline |
| `*.png, *.mp3` | Sample outputs from a previous run |

## Run it

Same environment as module 002.

Keys go in `handbook/.env` (copy `handbook/.env.example`). Notebooks load it with `load_dotenv(find_dotenv())`. Never paste keys into cells.

## Check your understanding

Add a dietary-restriction input and make sure every generated recipe respects it.

## Why it matters for Sentinel

First taste of a multi-step workflow, with a fixed sequence of model calls.

---

← [003 Prompt engineering](../003-prompt-engineering/) · [Handbook index](../) · [005 LangChain and retrieval-augmented generation (RAG)](../005-langchain-and-rag/) →
