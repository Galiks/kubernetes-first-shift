import uvicorn
from fastapi import FastAPI

from relayforge import config
from relayforge.logging import setup_logging
from relayforge.secrets import VERIFICATION_KEY_PATH, CONTROL_TOKEN_PATH, read_secret
from relayforge.test_sink import routes
from relayforge.test_sink.modes import ModeController
from relayforge.test_sink.receipts import ReceiptStore


class SinkState:
    verification_key: bytes = b""
    control_token: bytes = b""
    modes: ModeController = None  # type: ignore[assignment]
    receipts: ReceiptStore = None  # type: ignore[assignment]
    attempts: dict[str, int] = {}
    applied: set[str] = set()


state = SinkState()


def _lifespan(app: FastAPI):
    from contextlib import asynccontextmanager

    @asynccontextmanager
    async def _inner(app: FastAPI):
        setup_logging()
        state.verification_key = read_secret(VERIFICATION_KEY_PATH)
        state.control_token = read_secret(CONTROL_TOKEN_PATH)
        state.modes = ModeController()
        state.receipts = ReceiptStore()
        yield

    return _inner


app = FastAPI(lifespan=_lifespan(app))
app.include_router(routes.router)


def run() -> None:
    uvicorn.run(app, host="0.0.0.0", port=8080, log_config=None, access_log=False)