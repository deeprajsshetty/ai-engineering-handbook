# 000 · Python data foundations

**Level:** Beginner · **Prerequisites:** Basic Python.

## What you learn

- Create and inspect pandas Series and DataFrames
- Read and write CSV, Excel, JSON, JSONL, YAML and SQLite
- Filter, add and drop columns, and work with datetime indexes

## Files

| Path | Contents |
|---|---|
| `intro_to_pandas.ipynb` | The whole lesson, top to bottom |
| `traffic_data.csv, website_traffic_data_datetime.csv, model_logs.csv, slow_responses.csv` | Input datasets |
| `data1.json, data.xlsx, chat_data.jsonl, chat_data.yaml` | Inputs for the import and export exercises |

## Run it

Install `pandas openpyxl pyyaml` and open the notebook.

Keys go in `handbook/.env` (copy `handbook/.env.example`). Notebooks load it with `load_dotenv(find_dotenv())`. Never paste keys into cells.

## Check your understanding

Load `model_logs.csv`, then find the five slowest responses per model.

## Why it matters for Sentinel

Telemetry is tabular. The same skills read Prometheus or Loki exports later.

---

[Handbook index](../) · [001 FastAPI and MCP basics (todo API)](../001-fastapi-mcp-todo/) →
