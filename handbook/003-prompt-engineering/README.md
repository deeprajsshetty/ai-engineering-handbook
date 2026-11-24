# 003 · Prompt engineering

**Level:** Beginner · **Prerequisites:** Module 002.

## What you learn

- Write clear, specific instructions using delimiters and the RTF format
- Use few-shot prompting and step-by-step task specification
- Give the model time to think
- Reduce hallucinations with guarding instructions

## Files

| Path | Contents |
|---|---|
| `prompt_engineering_for_genai.ipynb` | All tactics with runnable examples |

## Run it

Same environment as module 002.

Keys go in `handbook/.env` (copy `handbook/.env.example`). Notebooks load it with `load_dotenv(find_dotenv())`. Never paste keys into cells.

## Check your understanding

Rewrite one vague prompt using delimiters and an output format, and compare the results.

## Why it matters for Sentinel

Sentinel's prompts (`llm/prompts.py`) wrap evidence in delimiters and forbid invented facts.

---

← [002 OpenAI APIs](../002-openai-apis/) · [Handbook index](../) · [004 Project: AI daily meal plan](../004-project-daily-meal-plan/) →
