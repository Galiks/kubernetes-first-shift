import httpx

async def deliver(url: str, body: bytes, headers: dict) -> tuple[int, float]:
    async with httpx.AsyncClient(
        timeout=httpx.Timeout(connect=5.0, read=10.0, write=5.0, pool=5.0),
        follow_redirects=False,
    ) as client:
        resp = await client.post(url, content=body, headers=headers)
        return resp.status_code, 0.0