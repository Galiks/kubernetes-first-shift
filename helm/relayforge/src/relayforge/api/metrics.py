from fastapi import APIRouter
from fastapi.responses import Response
from prometheus_client import Counter, Gauge, Histogram, generate_latest

router = APIRouter()

requests_total = Counter(
    "relayforge_requests_total", "HTTP requests processed by result", ["result"]
)
jobs_created_total = Counter("relayforge_jobs_created_total", "Delivery jobs created")
idempotency_conflicts_total = Counter(
    "relayforge_idempotency_conflicts_total", "Idempotency key conflicts"
)
kubernetes_errors_total = Counter(
    "relayforge_kubernetes_errors_total", "Kubernetes API errors by operation", ["operation"]
)
kubernetes_retries_total = Counter(
    "relayforge_kubernetes_retries_total", "Kubernetes client retries by reason", ["reason"]
)
request_duration_seconds = Histogram(
    "relayforge_request_duration_seconds", "HTTP request duration seconds", ["result"]
)
active_jobs = Gauge("relayforge_active_jobs", "Active delivery jobs observed by this pod")
oldest_active_job_seconds = Gauge(
    "relayforge_oldest_active_job_seconds", "Age in seconds of the oldest active delivery job"
)


def result_label(status_code: int) -> str:
    """Категория результата для metrics — ограниченный набор значений."""
    if status_code == 401:
        return "unauthorized"
    if status_code == 409:
        return "conflict"
    if status_code in (429, 503):
        return "backpressure"
    if 200 <= status_code < 300:
        return "success"
    if 400 <= status_code < 500:
        return "invalid"
    return "error"


def observe_request(status_code: int, duration: float) -> None:
    result = result_label(status_code)
    requests_total.labels(result=result).inc()
    request_duration_seconds.labels(result=result).observe(duration)


def inc_jobs_created() -> None:
    jobs_created_total.inc()


def inc_idempotency_conflict() -> None:
    idempotency_conflicts_total.inc()


def inc_k8s_error(operation: str) -> None:
    kubernetes_errors_total.labels(operation=operation).inc()


def inc_k8s_retry(reason: str) -> None:
    kubernetes_retries_total.labels(reason=reason).inc()


def set_active_jobs(count: int, oldest_seconds: float) -> None:
    active_jobs.set(count)
    oldest_active_job_seconds.set(oldest_seconds)


@router.get("/metrics")
async def metrics():
    return Response(generate_latest(), media_type="text/plain; version=0.0.4")