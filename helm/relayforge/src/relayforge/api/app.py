import asyncio
import signal
import time
from contextlib import asynccontextmanager

import uvicorn
from fastapi import FastAPI

from relayforge import config, logging as rlog
from relayforge.api import errors, metrics, probes, routes, ui
from relayforge.api.backpressure import Backpressure
from relayforge.api.job_registry import JobRegistry
from relayforge.api.k8s_client import create_k8s_client
from relayforge.api.state import state
from relayforge.secrets import CLIENT_TOKEN_PATH, read_secret

# Ссылка на запущенный uvicorn-сервер: используется обработчиком SIGTERM,
# чтобы перевести readiness в false немедленно и продолжить graceful shutdown.
SERVER: "uvicorn.Server | None" = None

METRICS_INTERVAL = 5.0


def _request_shutdown() -> None:
    """SIGTERM/SIGINT: немедленно readiness=false, затем graceful shutdown.

    uvicorn завершает активные запросы в пределах timeout_graceful_shutdown.
    Вынесено в отдельную функцию для юнит-тестирования обработчика.
    """
    state.ready = False
    if SERVER is not None:
        SERVER.should_exit = True


async def _metrics_loop() -> None:
    while True:
        if state.job_registry is not None:
            metrics.set_active_jobs(
                state.job_registry.active_count(),
                state.job_registry.oldest_active_seconds(),
            )
        await asyncio.sleep(METRICS_INTERVAL)


@asynccontextmanager
async def lifespan(app: FastAPI):
    rlog.setup_logging()
    state.client_token = read_secret(CLIENT_TOKEN_PATH)
    state.destinations = config.load_destinations()
    batch, core = await create_k8s_client()
    state.k8s = batch
    state.core = core
    state.job_registry = JobRegistry(batch, config.NAMESPACE, config.RELEASE)
    state.backpressure = Backpressure(
        max_active=config.API_BACKPRESSURE_MAX_ACTIVE,
        max_concurrent_create=config.API_BACKPRESSURE_MAX_CONCURRENT,
        registry=state.job_registry,
    )
    await state.job_registry.start()
    metrics_task = asyncio.create_task(_metrics_loop())

    loop = asyncio.get_running_loop()

    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, _request_shutdown)

    state.ready = True
    try:
        yield
    finally:
        state.ready = False
        metrics_task.cancel()
        try:
            await metrics_task
        except asyncio.CancelledError:
            pass
        await state.job_registry.stop()
        for sig in (signal.SIGTERM, signal.SIGINT):
            loop.remove_signal_handler(sig)


app = FastAPI(lifespan=lifespan)


@app.middleware("http")
async def metrics_middleware(request, call_next):
    start = time.monotonic()
    try:
        response = await call_next(request)
    except Exception:
        metrics.observe_request(500, time.monotonic() - start)
        raise
    metrics.observe_request(response.status_code, time.monotonic() - start)
    return response


app.include_router(routes.router)
app.include_router(probes.router)
app.include_router(metrics.router)
app.include_router(ui.router)
app.add_exception_handler(errors.ApiError, errors.api_error_handler)


async def main() -> None:
    global SERVER
    cfg = uvicorn.Config(
        app,
        host="0.0.0.0",
        port=config.API_PORT,
        log_config=None,
        access_log=False,
        timeout_graceful_shutdown=30,
    )
    server = uvicorn.Server(cfg)
    SERVER = server
    await server.serve()