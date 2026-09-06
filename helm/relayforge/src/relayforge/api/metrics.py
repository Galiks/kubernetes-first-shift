from fastapi import APIRouter
from fastapi.responses import Response
from prometheus_client import Counter, Histogram, Gauge, generate_latest

router = APIRouter()

requests_total = Counter("relayforge_requests_total", "...", ["result"])
jobs_created_total = Counter("relayforge_jobs_created_total", "...")
idempotency_conflicts_total = Counter("relayforge_idempotency_conflicts_total", "...")
kubernetes_errors_total = Counter("relayforge_kubernetes_errors_total", "...", ["operation"])
kubernetes_retries_total = Counter("relayforge_kubernetes_retries_total", "...", ["reason"])
request_duration_seconds = Histogram("relayforge_request_duration_seconds", "...")
active_jobs = Gauge("relayforge_active_jobs", "...")
oldest_active_job_seconds = Gauge("relayforge_oldest_active_job_seconds", "...")


@router.get("/metrics")
async def metrics():
    return Response(generate_latest(), media_type="text/plain; version=0.0.4")