"""Runbook knowledge base with BM25 retrieval.

Why BM25 instead of embeddings + a vector database: the corpus is a few dozen curated runbooks
whose vocabulary (signal names, action names, error terms) matches queries exactly. BM25 is
deterministic, explainable, has zero infrastructure and no embedding cost. Swap in a vector
store behind the same ``search`` method when the corpus grows (see docs/adr/0005).
"""

from __future__ import annotations

import math
import re
from collections import Counter
from dataclasses import dataclass, field
from pathlib import Path

import yaml

TOKEN_RE = re.compile(r"[a-z0-9_]+")
RUNBOOK_DIR = Path(__file__).parent / "runbooks"


def tokenize(text: str) -> list[str]:
    return TOKEN_RE.findall(text.lower())


def _str_list(value: object) -> list[str]:
    return [str(v) for v in value] if isinstance(value, list) else []


@dataclass
class Runbook:
    id: str
    title: str
    body: str
    signals: list[str] = field(default_factory=list)
    actions: list[str] = field(default_factory=list)

    @classmethod
    def parse(cls, path: Path) -> Runbook:
        raw = path.read_text()
        meta: dict[str, object] = {}
        body = raw
        if raw.startswith("---"):
            _, front, body = raw.split("---", 2)
            meta = yaml.safe_load(front) or {}
        return cls(
            id=path.stem,
            title=str(meta.get("title", path.stem)),
            body=body.strip(),
            signals=_str_list(meta.get("signals")),
            actions=_str_list(meta.get("actions")),
        )


@dataclass
class SearchHit:
    runbook: Runbook
    score: float


class RunbookRetriever:
    def __init__(self, runbooks: list[Runbook], k1: float = 1.5, b: float = 0.75) -> None:
        self.runbooks = runbooks
        self.k1, self.b = k1, b
        self.docs = [
            tokenize(f"{r.title} {' '.join(r.signals)} {' '.join(r.actions)} {r.body}")
            for r in runbooks
        ]
        self.avgdl = sum(len(d) for d in self.docs) / max(len(self.docs), 1)
        df: Counter[str] = Counter()
        for d in self.docs:
            df.update(set(d))
        n = len(self.docs)
        self.idf = {t: math.log(1 + (n - f + 0.5) / (f + 0.5)) for t, f in df.items()}
        self.tf = [Counter(d) for d in self.docs]

    @classmethod
    def from_directory(cls, directory: Path = RUNBOOK_DIR) -> RunbookRetriever:
        return cls([Runbook.parse(p) for p in sorted(directory.glob("*.md"))])

    def search(self, query: str, k: int = 3) -> list[SearchHit]:
        terms = tokenize(query)
        hits: list[SearchHit] = []
        for rb, tf, doc in zip(self.runbooks, self.tf, self.docs, strict=True):
            score = 0.0
            for t in terms:
                if t not in tf:
                    continue
                f = tf[t]
                denom = f + self.k1 * (1 - self.b + self.b * len(doc) / self.avgdl)
                score += self.idf.get(t, 0.0) * f * (self.k1 + 1) / denom
            if score > 0:
                hits.append(SearchHit(rb, round(score, 4)))
        hits.sort(key=lambda h: -h.score)
        return hits[:k]
