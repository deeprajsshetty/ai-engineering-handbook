# 005 · LangChain and retrieval-augmented generation (RAG)

**Level:** Intermediate · **Prerequisites:** Modules 002 and 003.

## What you learn

- Use LangChain models, prompt templates, caching, streaming and chains
- Use tools and ReAct agents (Python REPL, DuckDuckGo, Wikipedia)
- Split text, embed it, and store and search it in Pinecone and Chroma
- Build RAG over private documents with memory, and Streamlit front-ends

## Files

| Path | Contents |
|---|---|
| `01-langchain-deep-dive/` | A: LLMs, prompts, chains. B: tools, agents, ReAct |
| `02-vector-stores-pinecone/` | A: Pinecone basics. B: embeddings and similarity search |
| `03-langchain-with-gemini/` | LangChain with Google Gemini |
| `04-project-custom-chatgpt/` | Project: a ChatGPT clone with memory |
| `05-project-chatgpt-streamlit/` | Project: the same app with a Streamlit UI |
| `06-project-rag-private-documents/` | Project: Q&A over PDFs, DOCX and TXT, with Pinecone or Chroma; sample corpus in `files/` |
| `07-project-rag-streamlit-frontend/` | Project: Streamlit front-end for the RAG app |

## Run it

Each lesson folder has its own pinned `requirements.txt`. Use one virtualenv per folder if versions clash.

Keys go in `handbook/.env` (copy `handbook/.env.example`). Notebooks load it with `load_dotenv(find_dotenv())`. Never paste keys into cells.

## Check your understanding

In lesson 06, ask a question whose answer is *not* in the corpus. Make the app say so instead of guessing.

## Why it matters for Sentinel

Sentinel retrieves runbooks with BM25 (`knowledge/retriever.py`) and uses their metadata as a hard filter.

---

← [004 Project: AI daily meal plan](../004-project-daily-meal-plan/) · [Handbook index](../) · [006 Google Gemini API](../006-gemini-api/) →
