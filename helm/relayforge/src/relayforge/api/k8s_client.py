import asyncio
import logging
import time

from kubernetes_asyncio import client, config

from relayforge import config as rfconfig
from relayforge.api.errors import ApiError

logger = logging.getLogger(__name__)


class RetryBudget:
    """Общий бюджет повторов Kubernetes-вызовов: лимит в минуту и суммарный."""

    def __init__(self, max_per_minute: int = 60, max_total: int = 12):
        self.max_per_minute = max_per_minute
        self.max_total = max_total
        self._window: list[float] = []
        self._total = 0

    def try_acquire(self) -> bool:
        now = time.monotonic()
        self._window = [t for t in self._window if now - t < 60]
        if self._total >= self.max_total or len(self._window) >= self.max_per_minute:
            return False
        self._window.append(now)
        self._total += 1
        return True


async def create_k8s_client():
    """Возвращает (BatchV1Api, CoreV1Api) с общим connection pool и timeout."""
    config.load_incluster_config()
    cfg = client.Configuration.get_default_copy()
    cfg.connection_pool_maxsize = 50
    cfg.request_timeout = 10
    api_client = client.ApiClient(cfg)
    return client.BatchV1Api(api_client), client.CoreV1Api(api_client)


def _retry_after_seconds(exc: client.ApiException) -> float:
    value = None
    headers = exc.headers
    if headers:
        if isinstance(headers, dict):
            value = headers.get("Retry-After")
        else:
            for key, val in headers:
                if str(key).lower() == "retry-after":
                    value = val
                    break
    try:
        seconds = float(value)
    except (TypeError, ValueError):
        seconds = 5.0
    return min(max(seconds, 0.5), 30.0)


def _check_compatible(existing, job, canonical_hash: str) -> None:
    """Сверяет hash канонического запроса существующего Job с нашим."""
    actual = (existing.metadata.annotations or {}).get("relayforge/request-hash")
    if actual != canonical_hash:
        raise ApiError(409, "IDEMPOTENCY_CONFLICT", "idempotency key belongs to another request")


async def _read_job_or_none(batch, namespace: str, name: str):
    try:
        return await batch.read_namespaced_job(name, namespace)
    except client.ApiException as e:
        if e.status == 403:
            raise ApiError(500, "K8S_FORBIDDEN", "API cannot read jobs") from e
        if e.status == 404:
            return None
        raise


# Fault injection (только test profile, сценарий 02: неоднозначный результат create).
# Один раз: сервер принял Job, но клиент видит 5xx.
_fault_create_timeout_armed = False

if rfconfig.TEST_FAULT_CREATE_TIMEOUT:
    # Процесс поднят с test-флагом: взводим одноразовую fault injection.
    _fault_create_timeout_armed = True


def arm_create_fault() -> None:
    """Включает одноразовую fault injection на границе k8s client."""
    global _fault_create_timeout_armed
    _fault_create_timeout_armed = True


def _consume_create_fault() -> bool:
    global _fault_create_timeout_armed
    if rfconfig.TEST_FAULT_CREATE_TIMEOUT and _fault_create_timeout_armed:
        _fault_create_timeout_armed = False
        return True
    return False


async def create_job_idempotent(batch, namespace: str, job, budget: RetryBudget, canonical_hash: str):
    """Создаёт Job с гарантией «один Job на ключ».

    Возвращает (job, origin), origin in {"created", "existing"}.

    - 409 AlreadyExists: читаем существующий Job по детерминированному имени
      и сверяем hash запроса (конфликт — ApiError 409).
    - 429: ожидаем Retry-After; повторы ограничены бюджетом.
    - 403: retry не выполняется.
    - 5xx/timeout: читаем Job по имени — если создан, возвращаем его;
      иначе повторяем create.
    """
    while True:
        try:
            created = await batch.create_namespaced_job(namespace, job)
            if _consume_create_fault():
                # Fault injection (сценарий 02): API server принял Job, но
                # вызывающий код видит неопределённый результат. Клиент
                # получает 5xx; повторный HTTP-запрос найдёт Job по имени.
                raise ApiError(503, "CREATE_AMBIGUOUS",
                               "create result ambiguous (test fault injection)")
            return created, "created"
        except client.ApiException as e:
            if e.status == 403:
                raise ApiError(500, "K8S_FORBIDDEN", "API cannot create jobs") from e
            if e.status == 409:
                existing = await _read_job_or_none(batch, namespace, job.metadata.name)
                if existing is None:
                    continue  # Job успел исчезнуть до чтения — пробуем create ещё раз
                _check_compatible(existing, job, canonical_hash)
                return existing, "existing"
            if e.status == 429:
                if not budget.try_acquire():
                    raise ApiError(503, "RETRY_BUDGET_EXHAUSTED", "k8s retry budget exhausted")
                await asyncio.sleep(_retry_after_seconds(e))
                continue
            if e.status >= 500 or e.status == 0:  # 0 = таймаут на стороне клиента
                if not budget.try_acquire():
                    raise ApiError(503, "RETRY_BUDGET_EXHAUSTED", "k8s retry budget exhausted")
                existing = await _read_job_or_none(batch, namespace, job.metadata.name)
                if existing is not None:
                    _check_compatible(existing, job, canonical_hash)
                    return existing, "existing"
                continue
            raise