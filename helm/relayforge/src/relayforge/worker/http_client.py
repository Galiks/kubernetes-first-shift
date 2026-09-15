import httpx

from relayforge import config


async def deliver(url: str, body: bytes, headers: dict) -> tuple[int, float]:
    async with httpx.AsyncClient(
        timeout=httpx.Timeout(
            connect=config.WORKER_CONNECT_TIMEOUT,
            read=config.WORKER_READ_TIMEOUT,
            write=5.0,
            pool=5.0,
        ),
        follow_redirects=False,
    ) as client:
        resp = await client.post(url, content=body, headers=headers)
        return resp.status_code, 0.0