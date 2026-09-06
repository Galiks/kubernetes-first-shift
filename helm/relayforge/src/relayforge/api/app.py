import asyncio
import signal
from contextlib import asynccontextmanager

import uvicorn
from fastapi import FastAPI

from relayforge import config, logging as rlog
from relayforge.api import routes, probes, metrics, errors
from relayforge.api.k8s_client import create_k8s_client
from relayforge.api.backpressure import Backpressure
from relayforge.secrets import CLIENT_TOKEN_PATH, read_secret


class AppState:
    ready: bool = False
    destinations: dict = {}
    k8s = None
    backpressure: Backpressure = None  # type: ignore[assignment]
    client_token: bytes = b""


state = AppState()


@asynccontextmanager
async def lifespan(app: FastAPI):
    rlog.setup_logging()
    state.client_token = read_secret(CLIENT_TOKEN_PATH)
    state.destinations = config.load_destinations()
    state.k8s = await create_k8s_client()
    state.backpressure = Backpressure(
        max_active=config.API_BACKPRESSURE_MAX_ACTIVE,
        max_concurrent_create=config.API_BACKPRESSURE_MAX_CONCURRENT,
    )
    state.ready = True

    shutdown_event = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, shutdown_event.set)

    shutdown_task = asyncio.create_task(shutdown_event.wait())
    try:
        yield
    finally:
        state.ready = False
        shutdown_task.cancel()


app = FastAPI(lifespan=lifespan)
app.include_router(routes.router)
app.include_router(probes.router)
app.include_router(metrics.router)
app.add_exception_handler(errors.ApiError, errors.api_error_handler)


def run() -> None:
    uvicorn.run(
        app,
        host="0.0.0.0",
        port=config.API_PORT,
        log_config=None,
        access_log=False,
        timeout_graceful_shutdown=30,
    )