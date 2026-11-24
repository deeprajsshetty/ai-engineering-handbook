## What & why

## How it was tested
- [ ] `make test` (unit + integration)
- [ ] `make e2e` if alerting, agents or deployment changed

## Checklist
- [ ] Docs updated (README / ARCHITECTURE / API_DOCUMENTATION / CHANGELOG as relevant)
- [ ] New config has a safe default and is documented in `.env.example`
- [ ] No secrets, credentials or personal data in code, notebooks or fixtures
- [ ] Migrations added for schema changes (`alembic revision --autogenerate`) and reviewed
