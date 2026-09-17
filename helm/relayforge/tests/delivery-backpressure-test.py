"""Unit-тесты конкурентного лимита create и мягкого лимита активных Job.

Hermetic: без сети, без живого kubernetes-клиента (fakes).
"""

import asyncio
import os
import time

os.environ.setdefault("RELAYFORGE_RELEASE", "relay-test")
os.environ.setdefault("RELAYFORGE_NAMESPACE", "default")

import pytest

from relayforge.api import job_registry as jr
from relayforge.api import routes as r
from relayforge.api.backpressure import Backpressure
from relayforge.api.errors import ApiError
from relayforge.api.job_registry import JobRegistry
from relayforge.api.state import state


class FakeBatch:
    """Заглушка BatchV1Api: отслеживает попытки create (реально ничего нет)."""

    def __init__(self):
        self.create_calls = []

    async def list_namespaced_job(self, *args, **kwargs):
        return []

    async def create_namespaced_job(self, namespace, job):
        self.create_calls.append(job)
        return job


def _fresh_backpressure(max_active, max_concurrent):
    batch = FakeBatch()
    registry = JobRegistry(batch, "default", "relay-test")
    bp = Backpressure(
        max_active=max_active,
        max_concurrent_create=max_concurrent,
        registry=registry,
    )
    return bp, registry, batch


# --- по-Pod лимит одновременных create (asyncio.Semaphore) ------------------


def test_create_semaphore_blocks_above_limit():
    bp, _, _ = _fresh_backpressure(max_active=10, max_concurrent=2)

    async def scenario():
        # Semaphore построен с limit = max_concurrent_create
        assert bp.create_semaphore._value == 2
        await bp.create_semaphore.acquire()
        await bp.create_semaphore.acquire()
        assert bp.create_semaphore.locked()
        # Третий acquire блокируется, пока не освободится место.
        waiter = asyncio.create_task(bp.create_semaphore.acquire())
        await asyncio.sleep(0.05)
        assert not waiter.done(), "третий acquire должен блокироваться"
        bp.create_semaphore.release()
        assert await asyncio.wait_for(waiter, 1) is True

    asyncio.run(scenario())


def test_create_semaphore_allows_up_to_limit():
    bp, _, _ = _fresh_backpressure(max_active=10, max_concurrent=3)

    async def scenario():
        assert bp.create_semaphore._value == 3
        # Три слота захватываются сразу, четвёртый — блокируется.
        for _ in range(3):
            await bp.create_semaphore.acquire()
        assert bp.create_semaphore.locked()
        waiter = asyncio.create_task(bp.create_semaphore.acquire())
        await asyncio.sleep(0.05)
        assert not waiter.done()

    asyncio.run(scenario())


# --- мягкий лимит активных Job: превышение = ApiError, без create ------------


def test_active_limit_denies_extra_job_without_k8s_create():
    bp, registry, batch = _fresh_backpressure(max_active=2, max_concurrent=5)

    async def scenario():
        assert await bp.check_and_reserve("job-1") is True
        assert await bp.check_and_reserve("job-2") is True
        assert set(registry._pending) == {"job-1", "job-2"}
        with pytest.raises(ApiError) as ei:
            await bp.check_and_reserve("job-3")
        assert ei.value.status == 503
        assert ei.value.code == "BACKPRESSURE"
        assert ei.value.headers == {"Retry-After": "5"}
        # Резервирование не утекло: job-3 не попал в pending.
        assert "job-3" not in registry._pending
        # НИ один create в k8s не выполнялся.
        assert batch.create_calls == []

    asyncio.run(scenario())


def test_active_count_matches_nonterminal_jobs_plus_pending():
    """active_count() = non-terminal jobs из watch + pending, не подтверждённые
    watch (без AttributeError и согласованно с расчётом в try_reserve)."""
    bp, registry, _ = _fresh_backpressure(max_active=5, max_concurrent=5)

    async def scenario():
        assert registry.active_count() == 0
        assert await bp.check_and_reserve("job-a") is True
        assert await bp.check_and_reserve("job-b") is True
        # Оба — pending, ещё не подтверждены watch.
        assert registry._active_pending() == 2
        assert registry.active_count() == 2
        # watch подтвердил job-a как активный Job: pending снят, Job в _jobs.
        registry._pending.pop("job-a", None)
        registry._jobs["job-a"] = {"terminal": False, "created": time.time()}
        # 1 non-terminal Job + 1 pending (job-b, которого нет в _jobs).
        assert registry.active_count() == 2
        assert registry._active_pending() == 1
        # Согласовано с try_reserve: активных 2 при max_active=2 => блок.
        bp2, registry2, _ = _fresh_backpressure(max_active=2, max_concurrent=5)
        assert await bp2.check_and_reserve("x") is True
        assert await bp2.check_and_reserve("y") is True
        assert registry2.active_count() == 2
        with pytest.raises(ApiError):
            await bp2.check_and_reserve("z")

    asyncio.run(scenario())


def test_active_count_prunes_expired_pending():
    """Просроченный pending (старше _PENDING_TTL) не считается в active_count
    и удаляется из _pending после вызова (как в try_reserve)."""
    bp, registry, _ = _fresh_backpressure(max_active=5, max_concurrent=5)

    async def scenario():
        assert await bp.check_and_reserve("job-old") is True
        assert await bp.check_and_reserve("job-fresh") is True
        # job-old старше _PENDING_TTL, job-fresh — свежий.
        registry._pending["job-old"] = time.monotonic() - (jr._PENDING_TTL + 10.0)
        assert "job-old" in registry._pending
        assert registry.active_count() == 1
        # Просроченная запись удалена при подсчёте.
        assert "job-old" not in registry._pending
        assert registry._active_pending() == 1
        # job-fresh по-прежнему подтверждается.
        assert "job-fresh" in registry._pending

    asyncio.run(scenario())


def test_known_exempt_name_not_blocked():
    bp, _, _ = _fresh_backpressure(max_active=1, max_concurrent=5)

    async def scenario():
        assert await bp.check_and_reserve("job-k") is True
        # Повтор того же ключа (Job уже известен) лимит не блокирует.
        assert await bp.check_and_reserve("job-k") is False
        # Другой ключ при max_active=1 и активном job-k — уже блокируется.
        with pytest.raises(ApiError) as ei:
            await bp.check_and_reserve("job-other")
        assert ei.value.code == "BACKPRESSURE"

    asyncio.run(scenario())


# --- end-to-end: perform_create не создаёт Job при исчерпанном лимите ---------


def test_perform_create_denied_when_over_active_limit(monkeypatch):
    batch = FakeBatch()
    registry = JobRegistry(batch, "default", "relay-test")
    state.job_registry = registry
    state.backpressure = Backpressure(
        max_active=1, max_concurrent_create=2, registry=registry
    )

    async def fake_create(client, namespace, job, budget, canonical_hash):
        creates.append(1)
        return job, "created"

    monkeypatch.setattr(r, "create_job_idempotent", fake_create)
    creates = []
    try:
        status1, _ = asyncio.run(r.perform_create("key:one", {
            "destination": "test", "event_type": "a.b.c", "payload": {"n": 1},
        }))
        assert status1 == 202
        assert creates == [1]

        # Второй ключ (другой Job) упирается в лимит: ApiError и НИ одного create.
        with pytest.raises(ApiError) as ei:
            asyncio.run(r.perform_create("key:two", {
                "destination": "test", "event_type": "a.b.c", "payload": {"n": 2},
            }))
        assert ei.value.status == 503
        assert ei.value.code == "BACKPRESSURE"
        assert creates == [1], "превышение лимита не создаёт k8s Job"
    finally:
        state.job_registry = None
        state.backpressure = None