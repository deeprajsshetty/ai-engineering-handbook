"""Steady synthetic traffic against the frontend so there is always a baseline to compare to."""

from __future__ import annotations

import asyncio
import os
import random

import httpx

TARGET = os.environ.get("TARGET", "http://frontend:8080")
RPS = float(os.environ.get("RPS", "8"))


async def one(client: httpx.AsyncClient) -> None:
    try:
        if random.random() < 0.6:  # noqa: S311
            await client.get(f"{TARGET}/shop/browse")
        else:
            await client.post(f"{TARGET}/shop/checkout")
    except httpx.HTTPError:
        pass  # failures are what we are here to observe


async def main() -> None:
    print(f"loadgen: {RPS} rps -> {TARGET}", flush=True)
    async with httpx.AsyncClient(timeout=10) as client:
        while True:
            asyncio.create_task(one(client))
            await asyncio.sleep(random.expovariate(RPS))  # noqa: S311


if __name__ == "__main__":
    asyncio.run(main())
