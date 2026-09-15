"""Unit-тесты идемпотентного создания Job (k8s_client.create_job_idempotent).

Покрывают требования из RELAYFORGE_TASK.md: 409 AlreadyExists, 403 без retry,
429 с Retry-After, 5xx/timeout после успешного create, исчерпание retry budget,
fault injection на границе k8s client (сценарий 02).
"""

import asyncio
import os

os.environ.setdefault("RELAYFORGE_RELEASE", "relay-test")
os.environ.setdefault("RELAYFORGE_NAMESPACE", "default")

import pytest
from kubernetes_asyncio.client.exceptions import ApiException
from kubernetes_asyncio.client.models.v1_job import V1Job
from kubernetes_asyncio.client.models.v1_object_meta import V1ObjectMeta

from relayforge import config as rfconfig
from relayforge.api import k8s_client
from relayforge.api.errors import ApiError
from relayforge.api.k8s_client import RetryBudget, create_job_idempotent


def _job(name: str, request_hash: str) -> V1Job:
    return V1Job(
        api_version="batch/v1",
        kind="Job",
        metadata=V1ObjectMeta(
            name=name,
            annotations={"relayforge/request-hash": request_hash, "relayforge/delivery-id": "relay-a-x"},
        ),
    )


def _api_exc(status: int, headers=None) -> ApiException:
    exc = ApiException(status=status, reason="x")
    if headers is not None:
        exc.headers = list(headers.items())  # как в реальном клиенте: список кортежей
    return exc


def test_created_returns_original():
    expected = _job("j", "h1")
    calls = []

    class Batch:
        async def create_namespaced_job(self, namespace, job):
            calls.append("create")
            return expected

    job = _job("j", "h1")
    result, origin = asyncio.run(create_job_idempotent(Batch(), "ns", job, RetryBudget(), "h1"))
    assert origin == "created"
    assert result is expected
    assert calls == ["create"]


def test_409_existing_same_hash_returns_existing():
    existing = _job("j", "h1")

    class Batch:
        async def create_namespaced_job(self, namespace, job):
            raise _api_exc(409)

        async def read_namespaced_job(self, name, namespace):
            return existing

    job = _job("j", "h1")
    result, origin = asyncio.run(create_job_idempotent(Batch(), "ns", job, RetryBudget(), "h1"))
    assert origin == "existing"
    assert result is existing


def test_409_conflicting_hash_raises_conflict():
    existing = _job("j", "h-other")

    class Batch:
        async def create_namespaced_job(self, namespace, job):
            raise _api_exc(409)

        async def read_namespaced_job(self, name, namespace):
            return existing

    job = _job("j", "h1")
    with pytest.raises(ApiError) as ei:
        asyncio.run(create_job_idempotent(Batch(), "ns", job, RetryBudget(), "h1"))
    assert ei.value.status == 409
    assert ei.value.code == "IDEMPOTENCY_CONFLICT"


def test_403_raises_without_retry():
    calls = []

    class Batch:
        async def create_namespaced_job(self, namespace, job):
            calls.append("create")
            raise _api_exc(403)

    job = _job("j", "h1")
    with pytest.raises(ApiError) as ei:
        asyncio.run(create_job_idempotent(Batch(), "ns", job, RetryBudget(), "h1"))
    assert ei.value.status == 500
    # create вызван ровно один раз — retry для 403 не выполняется.
    assert calls == ["create"]


def test_429_retry_after_then_success():
    calls = []

    class Batch:
        async def create_namespaced_job(self, namespace, job):
            calls.append("create")
            if len(calls) == 1:
                raise _api_exc(429, {"Retry-After": "0"})
            return _job("j", "h1")

    job = _job("j", "h1")
    result, origin = asyncio.run(
        create_job_idempotent(Batch(), "ns", job, RetryBudget(max_total=5), "h1")
    )
    assert origin == "created"
    assert calls == ["create", "create"]


def test_5xx_then_read_found_returns_existing():
    existing = _job("j", "h1")
    calls = []

    class Batch:
        async def create_namespaced_job(self, namespace, job):
            calls.append("create")
            raise _api_exc(500)

        async def read_namespaced_job(self, name, namespace):
            calls.append("read")
            return existing

    job = _job("j", "h1")
    result, origin = asyncio.run(create_job_idempotent(Batch(), "ns", job, RetryBudget(), "h1"))
    assert origin == "existing"
    assert calls == ["create", "read"]


def test_timeout_then_404_then_create_success():
    calls = []

    class Batch:
        async def create_namespaced_job(self, namespace, job):
            calls.append("create")
            if len(calls) == 1:
                raise _api_exc(0)  # timeout на стороне клиента
            return _job("j", "h1")

        async def read_namespaced_job(self, name, namespace):
            calls.append("read")
            raise _api_exc(404)

    job = _job("j", "h1")
    result, origin = asyncio.run(
        create_job_idempotent(Batch(), "ns", job, RetryBudget(max_total=5), "h1")
    )
    assert origin == "created"
    assert calls == ["create", "read", "create"]


def test_retry_budget_exhaustion_raises_503():
    class Batch:
        async def create_namespaced_job(self, namespace, job):
            raise _api_exc(500)

        async def read_namespaced_job(self, name, namespace):
            raise _api_exc(404)

    job = _job("j", "h1")
    with pytest.raises(ApiError) as ei:
        asyncio.run(create_job_idempotent(Batch(), "ns", job, RetryBudget(max_total=1), "h1"))
    assert ei.value.status == 503
    assert ei.value.code == "RETRY_BUDGET_EXHAUSTED"

def test_fault_injection_timeout_after_accepted_create():
    """Сценарий 02: API server принял Job, вызывающий код получил 5xx.
    Порядок вызовов: create (принят) → клиент видит 503 CREATE_AMBIGUOUS,
    повторного create в первом запросе нет. Повторный HTTP-запрос: create
    (409 AlreadyExists) → read по имени → тот же Job, второго create нет."""
    created = _job("j", "h1")
    existing = _job("j", "h1")
    calls = []

    class Batch:
        async def create_namespaced_job(self, namespace, job):
            calls.append("create")
            return created

        async def read_namespaced_job(self, name, namespace):
            calls.append("read")
            return existing

    old = rfconfig.TEST_FAULT_CREATE_TIMEOUT
    rfconfig.TEST_FAULT_CREATE_TIMEOUT = True
    k8s_client.arm_create_fault()
    try:
        with pytest.raises(ApiError) as ei:
            asyncio.run(create_job_idempotent(Batch(), "ns", _job("j", "h1"), RetryBudget(), "h1"))
        assert ei.value.status == 503
        assert ei.value.code == "CREATE_AMBIGUOUS"
        assert calls == ["create"], "первый запрос: только один create, без read"

        # Повтор исходного HTTP-запроса с тем же ключом.
        calls2 = []

        class Batch2:
            async def create_namespaced_job(self, namespace, job):
                calls2.append("create")
                raise _api_exc(409)

            async def read_namespaced_job(self, name, namespace):
                calls2.append("read")
                return existing

        result, origin = asyncio.run(
            create_job_idempotent(Batch2(), "ns", _job("j", "h1"), RetryBudget(), "h1")
        )
        assert origin == "existing"
        assert result is existing
        assert calls2 == ["create", "read"], "второго create нет: read после 409"
    finally:
        rfconfig.TEST_FAULT_CREATE_TIMEOUT = old
