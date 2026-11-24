# syntax=docker/dockerfile:1.7
# Sentinel API / worker image. Same image, different command:
#   api:     uvicorn --factory sentinel.api.app:app_factory
#   worker:  sentinel worker
#   migrate: sentinel migrate

FROM python:3.12-slim AS builder
COPY --from=ghcr.io/astral-sh/uv:0.8.4 /uv /usr/local/bin/uv
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy UV_PYTHON_DOWNLOADS=never
WORKDIR /app
# Dependencies first (cached layer), from the lock file only.
COPY pyproject.toml uv.lock README.md ./
RUN --mount=type=cache,target=/root/.cache/uv uv sync --frozen --no-dev --no-install-project
COPY src ./src
RUN --mount=type=cache,target=/root/.cache/uv uv sync --frozen --no-dev --no-editable

FROM python:3.12-slim AS runtime
LABEL org.opencontainers.image.title="sentinel" \
      org.opencontainers.image.description="AI-agentic observability platform" \
      org.opencontainers.image.source="https://github.com/deeprajsshetty/ai-engineering-handbook"
RUN groupadd --system --gid 10001 sentinel \
 && useradd --system --uid 10001 --gid sentinel --home-dir /app --shell /usr/sbin/nologin sentinel
WORKDIR /app
COPY --from=builder --chown=sentinel:sentinel /app/.venv /app/.venv
ENV PATH="/app/.venv/bin:$PATH" PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1
USER 10001
EXPOSE 8000 9100
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=3 \
  CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8000/health/live', timeout=2).status == 200 else 1)"
CMD ["uvicorn", "--factory", "sentinel.api.app:app_factory", "--host", "0.0.0.0", "--port", "8000", "--proxy-headers", "--no-server-header"]
