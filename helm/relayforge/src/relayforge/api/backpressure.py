import asyncio

from relayforge.api.errors import ApiError


class Backpressure:
    def __init__(self, max_active: int, max_concurrent_create: int):
        self.max_active = max_active
        self.create_semaphore = asyncio.Semaphore(max_concurrent_create)
        self._active = 0

    def set_active(self, n: int) -> None:
        self._active = n

    async def acquire(self) -> None:
        if self._active >= self.max_active:
            raise ApiError(429, "BACKPRESSURE", "too many active jobs",
                           headers={"Retry-After": "5"})
        await self.create_semaphore.acquire()

    def release(self) -> None:
        self.create_semaphore.release()