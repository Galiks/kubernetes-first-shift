import hashlib
import datetime

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse

from relayforge import config
from relayforge.api.app import state
from relayforge.api.auth import verify_token
from relayforge.api.validation import validate_request
from relayforge.api.job_builder import job_name, new_delivery_id, build_job
from relayforge.api.k8s_client import create_job_with_retry, RetryBudget
from relayforge.api.backpressure import Backpressure
from relayforge.api.errors import ApiError
from relayforge.canonical import canonicalize

import orjson
import logging
logger = logging.getLogger(__name__)

router = APIRouter()
retry_budget = RetryBudget()


@router.post("/v1/deliveries", status_code=202)
async def create_delivery(request: Request):
    await verify_token(request)
    idem_key, data, _ = await validate_request(request)

    canonical = canonicalize({
        "destination": data["destination"],
        "event_type": data["event_type"],
        "payload": data["payload"],
    })
    idem_key_hash = hashlib.sha256(idem_key.encode("utf-8")).hexdigest()
    name = job_name(config.RELEASE, idem_key)
    delivery_id = new_delivery_id(config.RELEASE)

    await state.backpressure.acquire()
    try:
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
            destinations_configmap=f"{config.RELEASE}-relayforge-destinations",
            backoff_limit=config.WORKER_BACKOFF_LIMIT,
            active_deadline=config.WORKER_ACTIVE_DEADLINE,
            ttl_after_finished=config.WORKER_TTL,
            permanent_exit_code=config.WORKER_PERMANENT_EXIT_CODE,
            secret_revision=config.SECRET_REVISION,
        )
        try:
            created, origin = await create_job_with_retry(
                state.k8s, config.NAMESPACE, job, retry_budget,
            )
        except Exception:
            raise
        final_id = created.metadata.annotations["relayforge/delivery-id"]
        duplicate = origin == "existing_after_error" or (
            created.metadata.annotations.get("relayforge/request-hash")
            != hashlib.sha256(canonical).hexdigest()
            and False  # не должно случиться — см. ниже
        )
        # Если request-hash совпадает — это duplicate
        if created.metadata.annotations.get("relayforge/request-hash") == hashlib.sha256(canonical).hexdigest():
            if origin != "created":
                duplicate = True
        else:
            raise ApiError(409, "IDEMPOTENCY_CONFLICT", "idempotency key belongs to another request")

        status_code = 200 if duplicate else 202
        return JSONResponse(
            status_code=status_code,
            content={
                "id": final_id,
                "status": _job_status(created),
                "attempts": 0,
                "duplicate": duplicate,
                "created_at": created.metadata.annotations["relayforge/created-at"],
            },
        )
    finally:
        state.backpressure.release()


@router.get("/v1/deliveries/{delivery_id}")
async def get_delivery(delivery_id: str):
    await verify_token(request)  # type: ignore[name-defined]
    # list Jobs по label relayforge/delivery-id
    jobs = await state.k8s.list_namespaced_job(
        config.NAMESPACE,
        label_selector=f"relayforge/delivery-id={delivery_id}",
    )
    if not jobs.items:
        raise ApiError(404, "DELIVERY_NOT_FOUND", "job no longer exists")
    job = jobs.items[0]
    # Подсчёт attempts по Pods
    pods = await state.core.list_namespaced_pod(
        config.NAMESPACE,
        label_selector=f"job-name={job.metadata.name}",
    )
    attempts = len(pods.items)
    retained_until = None
    if job.status and any(c.type in ("Complete", "Failed") for c in (job.status.conditions or [])):
        finished = job.status.completion_time or job.status.start_time
        if finished:
            retained_until = (finished + datetime.timedelta(seconds=config.WORKER_TTL)).isoformat()
    return {
        "id": delivery_id,
        "status": _job_status(job),
        "attempts": attempts,
        "destination": job.spec.template.spec.containers[0].env[1].value,
        "created_at": job.metadata.annotations["relayforge/created-at"],
        "finished_at": (job.status.completion_time.isoformat() if job.status and job.status.completion_time else None),
        "retained_until": retained_until,
        "failure": _job_failure(job),
    }


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