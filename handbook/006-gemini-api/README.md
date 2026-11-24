# 006 · Google Gemini API

**Level:** Intermediate · **Prerequisites:** Modules 002 and 003.

## What you learn

- Generate text and multimodal output, stream it, and tune generation parameters
- Build chat conversations and a chatbot
- Use system instructions, the File API (images and audio), and token counting
- Apply prompt engineering to Gemini

## Files

| Path | Contents |
|---|---|
| `01_ to 05_ notebooks` | Lessons in order; the images and `sample.mp3` at the module root are their inputs |
| `project-rename-images/` | Project: rename photos from Gemini vision descriptions |
| `project-talk-with-an-image/` | Project: Streamlit 'talk to a photo' app |

## Run it

Install `requirements.txt` and set `GOOGLE_API_KEY`.

Keys go in `handbook/.env` (copy `handbook/.env.example`). Notebooks load it with `load_dotenv(find_dotenv())`. Never paste keys into cells.

## Check your understanding

Run the same prompt through Gemini and OpenAI, then compare cost, latency and output.

## Why it matters for Sentinel

Being able to swap providers is why Sentinel hides models behind one `LLMProvider` interface.

---

← [005 LangChain and retrieval-augmented generation (RAG)](../005-langchain-and-rag/) · [Handbook index](../) · [007 LangGraph fundamentals](../007-langgraph-fundamentals/) →
