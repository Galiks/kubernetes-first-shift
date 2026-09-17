"""Локальный наблюдатель активных delivery-Jobs release (namespace-scoped watch).

Подсчёт не требует cluster-wide list: watch ограничен namespace и
label-селектором instance+component=delivery. Несколько API-Pods видят свои
копии состояния — лимит мягкий, возможное превышение ограничено и измеряется
(см. сценарий backpressure-ha).
"""

import asyncio
import datetime
import logging
import time

from kubernetes_asyncio import watch as k8s_watch

from relayforge.api.errors import ApiError

logger = logging.getLogger(__name__)

_WATCH_SECONDS = 120
_PRUNE_TTL_MULTIPLIER = 2
# Время жизни локальной «pending» записи о созданном этим Pod Job'е.
_PENDING_TTL = 30.0


class JobRegistry:
    def __init__(self, batch, namespace: str, release: str):
        self._batch = batch
        self._namespace = namespace
        self._selector = (
            f"app.kubernetes.io/instance={release},"
            f"app.kubernetes.io/component=delivery"
        )
        self._jobs: dict[str, dict] = {}
        # Локальный (мгновенный) учёт Job, созданных этим API-Pod, до того
        # как watch подтвердит их. Уменьшает превышение мягкого лимита до
        # окна гонки check->create.
        self._pending: dict[str, float] = {}
        self._lock = asyncio.Lock()
        self._task: asyncio.Task | None = None

    async def start(self) -> None:
        self._task = asyncio.create_task(self._run())

    async def stop(self) -> None:
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None

    async def _run(self) -> None:
        while True:
            w = k8s_watch.Watch()
            try:
                async for event in w.stream(
                    self._batch.list_namespaced_job,
                    self._namespace,
                    label_selector=self._selector,
                    timeout_seconds=_WATCH_SECONDS,
                ):
                    await self._record(event)
            except asyncio.CancelledError:
                raise
            except Exception as e:
                status = getattr(e, "status", None)
                logger.warning("job watch error; restarting: %s status=%s", type(e).__name__, status)
                await asyncio.sleep(5)
            finally:
                w.stop()
                await w.close()

    async def _record(self, event) -> None:
        obj = event.get("object")
        if obj is None or obj.metadata is None or obj.metadata.name is None:
            return
        name = obj.metadata.name
        async with self._lock:
            # watch подтвердил Job — локальная pending-запись больше не нужна
            self._pending.pop(name, None)
            if event.get("type") == "DELETED":
                self._jobs.pop(name, None)
                return
            terminal = _is_terminal(obj)
            self._jobs[name] = {"terminal": terminal, "created": _creation_timestamp(obj)}
            _prune(self._jobs, ttl=_PRUNE_TTL_MULTIPLIER)

    def release(self, name: str) -> None:
        """Снимает локальное резервирование (create не состоялся / Job уже известен)."""
        self._pending.pop(name, None)

    async def try_reserve(self, max_active: int, exempt_name: str | None) -> bool:
        """Атомарно: проверить мягкий лимит активных Job и зарезервировать
        место под новый Job (pending). Возвращает True, если место занято.

        Известный Job (повтор ключа) лимит не блокирует — вернёт False
        без резервирования. Вызов сериализован локальным lock, поэтому
        в пределах одного API-Pod лимит соблюдается точно; превышение
        возможно только между Pod'ами (HA) — см. сценарий 04.
        """
        async with self._lock:
            now = time.monotonic()
            for n, ts in list(self._pending.items()):
                if now - ts > _PENDING_TTL:
                    self._pending.pop(n, None)
            if exempt_name is not None and (
                exempt_name in self._jobs or exempt_name in self._pending
            ):
                return False
            active = sum(1 for j in self._jobs.values() if not j["terminal"])
            active += sum(1 for n in self._pending if n not in self._jobs)
            if active >= max_active:
                raise ApiError(
                    503,
                    "BACKPRESSURE",
                    "too many active jobs",
                    headers={"Retry-After": "5"},
                )
            if exempt_name is not None:
                self._pending[exempt_name] = now
            return True

    def active_count(self) -> int:
        """Число delivery-Jobs без terminal condition (queued/running),
        включая только что созданные этим Pod (pending)."""
        return sum(1 for j in self._jobs.values() if not j["terminal"]) + self._active_pending()

    def _active_pending(self) -> int:
        """Число pending-резервирований, ещё не подтверждённых watch.

        Согласовано с расчётом активных в try_reserve: pending-запись об
        уже известном (подтверждённом watch) Job не учитывается дважды.
        Просроченные записи (старше _PENDING_TTL) удаляются здесь по той же
        границе, что и в try_reserve: иначе брошенный pending (create не
        удался, release() не вызван, watch Job не подтвердил) завышал бы
        метрику active_jobs бессрочно.
        """
        now = time.monotonic()
        for name, ts in list(self._pending.items()):
            if now - ts > _PENDING_TTL:
                self._pending.pop(name, None)
        return sum(1 for n in self._pending if n not in self._jobs)

    def has(self, name: str) -> bool:
        """Известен ли Job с таким именем (детерминированное имя по ключу)."""
        return name in self._jobs or name in self._pending

    def oldest_active_seconds(self) -> float:
        now = time.time()
        oldest = None
        for j in self._jobs.values():
            if not j["terminal"]:
                created = j.get("created") or now
                if oldest is None or created < oldest:
                    oldest = created
        if oldest is None:
            return 0.0
        return max(0.0, now - oldest)


def _is_terminal(job) -> bool:
    conditions = (job.status.conditions or []) if job.status else []
    for cond in conditions:
        if cond.type in ("Complete", "Failed") and cond.status == "True":
            return True
    return False


def _creation_timestamp(job) -> float:
    ts = job.metadata.creation_timestamp
    if ts is not None:
        return ts.timestamp()
    created_at = (job.metadata.annotations or {}).get("relayforge/created-at")
    if created_at:
        try:
            return datetime.datetime.fromisoformat(created_at.replace("Z", "+00:00")).timestamp()
        except ValueError:
            pass
    return time.time()


def _prune(jobs: dict, ttl: float) -> None:
    """Удаляет завершённые Jobs, которые TTL-контроллер уже должен был убрать."""
    now = time.time()
    for name in list(jobs):
        j = jobs[name]
        if j["terminal"] and now - (j.get("created") or now) > ttl * 600:
            del jobs[name]