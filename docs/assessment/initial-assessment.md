# Initial assessment and roadmap

Date: 2026-11-10. This is the state of the repository **before** the transformation, and the plan
that followed from it. It is kept as a record; the current state is in
[PROJECT_KNOWLEDGE.md](../../PROJECT_KNOWLEDGE.md).

## 1. What existed

| Item | Finding |
|---|---|
| Git history | One commit (`Initial commit`) with `.gitignore`, `LICENSE` and `README.md`. **Everything else was untracked.** |
| Content | Eight numbered learning modules (001–008): about 40 Jupyter notebooks, 4 small Python apps (one FastAPI + MCP API, three Streamlit apps), PDFs, images and audio. Loose pandas tutorial data files at the root. |
| Runnable services | `001-fastapi-mcp-todo/main.py`: a FastAPI CRUD API on SQLite, exposed as MCP tools via `fastapi-mcp`. Clean code, but no tests and no auth. |
| Deployment | `render.yaml` for the todo app, with `rootDir: fastapi-mcp-todo`. **That path did not exist** (the folder was `001-fastapi-mcp-todo`), so the blueprint could not deploy. |
| Environments | A 357 MB virtualenv (`genai/`) at the root and a `.venv` inside module 001. Neither was ignored by name. |
| Dependencies | Per-module `requirements.txt`, some fully pinned (001, 008), some unpinned. No lock files. Old-library copies kept in `code_for_old_library_version/` folders. |
| Tests, CI, containers, docs | None (the README was 2 lines). |

## 2. Issues found

| # | Severity | Issue | Resolution |
|---|---|---|---|
| S1 | **Critical** | Hard-coded OpenAI project keys (`sk-proj-…`) in four notebooks (code cells and saved outputs) and a LangSmith key (`lsv2_pt_…`) in a fifth, plus copies in `.ipynb_checkpoints` | Redacted; code reads `os.getenv`. **Owner must rotate the keys** ([SECURITY.md §6](../../SECURITY.md#6-security-history-of-this-repository)). Not in git history, so no history rewrite is needed. |
| S2 | High | `.gitignore` did not cover `genai/`, `*.db`, `.langchain.db`, `.DS_Store`; it contained a broad `lib/` rule | Rewritten, curated `.gitignore` |
| S3 | Medium | No pre-commit or secret scanning; notebook outputs committed with secrets | gitleaks + nbstripout + detect-private-key hooks; gitleaks in CI |
| D1 | High | `render.yaml` pointed at a non-existent folder | Fixed to `handbook/001-fastapi-mcp-todo` |
| D2 | Medium | Duplicate files: the same PDFs and images in several folders; notebooks duplicated between 003 and 004 | Left in place. They are learning material and their relative paths are used. Flagged for a later media clean-up. |
| D3 | Medium | Loose data files at the root, which are outputs of `intro_to_pandas.ipynb` | Moved with the notebook to `handbook/000-python-data-foundations/` |
| T1 | High | No tests, CI or containers anywhere | Added for the new platform; the handbook stays learning material |
| A1 | Info | The repository was a learning handbook, not an observability codebase. The request asked for an observability platform as the primary implementation. | Build Sentinel as a new top-level component and preserve the handbook unchanged (assumption A6 in the [discovery record](../fde/customer-discovery.md#9-assumptions-and-ambiguities)) |

## 3. Functionality to preserve

- Every notebook and app in modules 001–008, with relative paths intact (whole folders moved).
- The todo API's behaviour and its MCP tool names (verified after the move).
- The Render deployment of the todo app (path fixed).

## 4. Roadmap (as executed)

| Step | Scope | Exit check |
|---|---|---|
| 1 | Hygiene: restructure, scrub secrets, `.gitignore`, env templates | `git status --ignored` shows no secrets, venvs or databases tracked; notebooks still valid JSON |
| 2 | Platform foundation: package layout, config, DB + migrations, auth | lint + types clean; migration matches models |
| 3 | Domain + agents: anomaly, correlation, RCA, traces, LangGraph pipeline, RAG, LLM adapters | Scenario tests pass |
| 4 | API + worker + console + MCP | Integration tests pass (HITL, auth, audit) |
| 5 | Telemetry stack + demo workload + dashboards + alert rules | Live metric, log and trace names verified against the query templates |
| 6 | E2E, load and resilience validation | e2e passes; load targets met |
| 7 | Kubernetes, CI/CD, docs | Overlays render; docs complete and linked |

All seven steps were completed; see the [validation report](../testing/test-strategy.md#validation-report).
