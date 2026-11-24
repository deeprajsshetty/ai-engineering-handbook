# 012 · Capstone: research agent

**Level:** Advanced capstone · **Prerequisites:** Modules 005 and 007.

## What you learn

- Build an ArXiv ingestion and PDF chunking pipeline
- Build a Pinecone knowledge base
- Build a LangGraph oracle agent with ArXiv, SerpAPI and RAG tools
- Generate a final report

## Files

| Path | Contents |
|---|---|
| `01-project_research_agent_langgraph_arxiv_serpapi.ipynb` | The full project |
| `files/arxiv_dataset.json` | Cached ArXiv metadata |
| `requirements.txt` | Dependencies |

## Run it

Install `requirements.txt` and set `OPENAI_API_KEY`, `PINECONE_API_KEY` and `SERPAPI_KEY`.

Keys go in `handbook/.env` (copy `handbook/.env.example`). Notebooks load it with `load_dotenv(find_dotenv())`. Never paste keys into cells.

## Check your understanding

Add a tool-call budget, and stop the agent with a partial report when it runs out.

## Why it matters for Sentinel

Next step: see the same ideas hardened for production in [Sentinel](../../sentinel/).

---

← [011 LangGraph projects](../011-langgraph-projects/) · [Handbook index](../) · [Sentinel](../../sentinel/) →
