import asyncio
import time

from kubernetes_asyncio import client, config

from relayforge.api.errors import ApiError
from relayforge.logging import setup_logging

import logging
logger = logging.getLogger(__name__)


class RetryBudget:
    def __init__(self, max_per_minute: int = 60):
        self.max_per_minute = max_per_minute
        self._events: list[float] = []

    def try_acquire(self) -> bool:
        now = time.monotonic()
        self._events = [t for t in self._events if now - t < 60]
        if len(self._events) >= self.max_per_minute:
            return False
        self._events.append(now)
        return True


async def create_k8s_client():
    config.load_incluster_config()
    cfg = client.Configuration.get_default_copy()
    cfg.connection_pool_maxsize = 50
    cfg.request_timeout = 10
    api_client = client.ApiClient(cfg)
    return client.BatchV1Api(api_client)


async def create_job_with_retry(
    batch: client.BatchV1Api,
    namespace: str,
    job,
    budget: RetryBudget,
):
    attempt = 0
    while True:
        try:
            created = await batch.create_namespaced_job(namespace, job)
            return created, "created"
        except client.ApiException as e:
            if e.status == 409:
                raise  # обрабатывается выше
            if e.status == 403:
                raise ApiError(500, "K8S_FORBIDDEN", "API cannot create jobs")
            if e.status == 429:
                retry_after = int(e.headers.get("Retry-After", "5"))
                logger.warning("k8s 429", extra={"retry_after": retry_after})
                await asyncio.sleep(retry_after)
                continue
            if e.status >= 500 or e.status == 0:  # timeout
                if not budget.try_acquire():
                    raise ApiError(503, "RETRY_BUDGET_EXHAUSTED", "too many k8s retries")
                # прочитать Job по детерминированному имени
                try:
                    existing = await batch.read_namespaced_job(job.metadata.name, namespace)
                    return existing, "existing_after_error"
                except client.ApiException as e2:
                    if e2.status == 404:
                        attempt += 1
                        if attempt > 3:
                            raise ApiError(503, "K8S_UNAVAILABLE", "cannot confirm job creation")
                        await asyncio.sleep(0.5 * (2 ** attempt))
                        continue
                    raise
            raise