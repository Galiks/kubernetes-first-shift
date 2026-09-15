import asyncio


class Backpressure:
    """Мягкий release-scoped лимит активных delivery-Jobs + лимит одновременных
    create на один API-Pod.

    Проверка лимита и резервирование места под Job выполняются атомарно
    в JobRegistry.try_reserve (локальный per-Pod lock): в пределах одного Pod
    лимит соблюдается точно. Превышение возможно только между Pod'ами (HA)
    и ограничено числом Pod'ов — измеряется в сценарии 04. Подсчёт активных —
    локальный watch + pending, без cluster-wide list.
    """

    def __init__(self, max_active: int, max_concurrent_create: int, registry):
        self.max_active = max_active
        self.registry = registry
        self.create_semaphore = asyncio.Semaphore(max_concurrent_create)

    async def check_and_reserve(self, exempt_name: str | None) -> bool:
        """Проверяет лимит и резервирует место; True — место занято.

        Повтор ключа с уже известным Job (exempt_name известен registry)
        лимит не блокирует: вернёт False без резервирования.
        """
        if self.max_active <= 0:
            return False
        return await self.registry.try_reserve(self.max_active, exempt_name)