import datetime
import hashlib
import logging
import time

import orjson
from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse

from relayforge import config
from relayforge.api import metrics
from relayforge.api.auth import verify_token
from relayforge.api.errors import ApiError
from relayforge.api.job_builder import build_job, job_name, new_delivery_id
from relayforge.api.k8s_client import RetryBudget, create_job_idempotent
from relayforge.api.state import state
from relayforge.api.validation import validate_request
from relayforge.canonical import canonicalize

logger = logging.getLogger(__name__)

router = APIRouter()
retry_budget = RetryBudget()


@router.post("/v1/deliveries", status_code=202)
async def create_delivery(request: Request):
    await verify_token(request)
    idem_key, data, _ = await validate_request(request)
    status_code, payload = await perform_create(idem_key, data)
    return JSONResponse(status_code=status_code, content=payload)


async def perform_create(idem_key: str, data: dict) -> tuple[int, dict]:
    """Общая логика принятия доставки (используется API-роутом и UI).

    Возвращает (http_status, тело ответа). Гарантия «один Job на ключ»
    обеспечивается create_job_idempotent + атомарным резервированием лимита.
    """
    start = time.monotonic()

    canonical = canonicalize({
        "destination": data["destination"],
        "event_type": data["event_type"],
        "payload": data["payload"],
    })
    canonical_hash = hashlib.sha256(canonical).hexdigest()
    idem_key_hash = hashlib.sha256(idem_key.encode("utf-8")).hexdigest()
    name = job_name(config.RELEASE, idem_key)
    delivery_id = new_delivery_id(config.RELEASE)

    job = build_job(
        release=config.RELEASE,
        namespace=config.NAMESPACE,
        name=name,
        delivery_id=delivery_id,
        canonical_bytes=canonical,
        idem_key_hash=idem_key_hash,
        destination=data["destination"],
        event_type=data["event_type"],
        payload_json=orjson.dumps(data["payload"]).decode("utf-8"),
        image=config.IMAGE_REPOSITORY,
        image_digest=config.IMAGE_DIGEST,
        signing_secret_name=config.SIGNING_SECRET_NAME,
        signing_secret_key=config.SIGNING_SECRET_KEY,
        destinations_configmap=config.DESTINATIONS_CONFIGMAP,
        worker_service_account=config.WORKER_SERVICE_ACCOUNT,
        backoff_limit=config.WORKER_BACKOFF_LIMIT,
        active_deadline=config.WORKER_ACTIVE_DEADLINE,
        ttl_after_finished=config.WORKER_TTL,
        permanent_exit_code=config.WORKER_PERMANENT_EXIT_CODE,
        secret_revision=config.SECRET_REVISION,
    )

    # Мягкий лимит активных Job (локальное наблюдение, не строгая граница).
    # Проверка лимита и резервирование — атомарно и сразу перед create
    # (внутри семафора параллельных create): в пределах Pod лимит точный.
    # Повтор ключа с уже известным Job лимит не блокирует.
    try:
        async with state.backpressure.create_semaphore:
            reserved = await state.backpressure.check_and_reserve(exempt_name=name)
            try:
                created, origin = await create_job_idempotent(
                    state.k8s, config.NAMESPACE, job, retry_budget, canonical_hash,
                )
            except BaseException:
                if reserved:
                    state.job_registry.release(name)
                raise
    except ApiError as e:
        if e.status == 409:
            metrics.inc_idempotency_conflict()
        raise

    final_id = created.metadata.annotations["relayforge/delivery-id"]
    duplicate = origin == "existing"
    status_code = 200 if duplicate else 202
    if not duplicate:
        metrics.inc_jobs_created()
    else:
        # Job уже существует и подтверждён create-путём: локальный резерв снять
        # (watch вскоре подтвердит Job сам — pending исчезнет и без этого).
        state.job_registry.release(name)

    payload = {
        "id": final_id,
        "status": _job_status(created),
        "attempts": 0,
        "duplicate": duplicate,
        "created_at": created.metadata.annotations["relayforge/created-at"],
    }
    metrics.observe_request(status_code, time.monotonic() - start)
    return status_code, payload


@router.get("/v1/deliveries/{delivery_id}")
async def get_delivery(request: Request, delivery_id: str):
    await verify_token(request)
    jobs = await state.k8s.list_namespaced_job(
        config.NAMESPACE,
        label_selector=f"relayforge/delivery-id={delivery_id}",
    )
    if not jobs.items:
        raise ApiError(404, "DELIVERY_NOT_FOUND", "job no longer exists")
    job = jobs.items[0]

    # Число попыток = число запусков worker (Pods, созданных Job-контроллером).
    pods = await state.core.list_namespaced_pod(
        config.NAMESPACE,
        label_selector=f"job-name={job.metadata.name}",
    )
    attempts = len(pods.items)

    retained_until = None
    if job.status and any(
        c.type in ("Complete", "Failed") and c.status == "True"
        for c in (job.status.conditions or [])
    ):
        finished = job.status.completion_time or job.status.start_time
        if finished:
            retained_until = (
                finished + datetime.timedelta(seconds=config.WORKER_TTL)
            ).isoformat()

    return {
        "id": delivery_id,
        "status": _job_status(job),
        "attempts": attempts,
        "destination": _job_destination(job),
        "created_at": job.metadata.annotations.get("relayforge/created-at"),
        "finished_at": (
            job.status.completion_time.isoformat()
            if job.status and job.status.completion_time
            else None
        ),
        "retained_until": retained_until,
        "failure": _job_failure(job),
    }


def _job_destination(job):
    try:
        return job.spec.template.spec.containers[0].env[1].value
    except (AttributeError, IndexError):
        return None


def _job_status(job) -> str:
    if not job.status or not job.status.conditions:
        return "queued"
    for c in job.status.conditions:
        if c.type == "Complete" and c.status == "True":
            return "succeeded"
        if c.type == "Failed" and c.status == "True":
            return "failed"
    if job.status.active:
        return "running"
    return "queued"


def _job_failure(job):
    if not job.status or not job.status.conditions:
        return None
    for c in job.status.conditions:
        if c.type == "Failed" and c.status == "True":
            return {"code": "JOB_FAILED", "message": c.message or "job failed"}
    return None