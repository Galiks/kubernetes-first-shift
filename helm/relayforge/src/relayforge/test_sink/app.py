from contextlib import asynccontextmanager

import uvicorn
from fastapi import FastAPI

from relayforge import config
from relayforge.api.errors import ApiError, api_error_handler
from relayforge.logging import setup_logging
from relayforge.secrets import CONTROL_TOKEN_PATH, VERIFICATION_KEY_PATH, read_secret
from relayforge.test_sink import routes
from relayforge.test_sink.modes import ModeController
from relayforge.test_sink.receipts import ReceiptStore
from relayforge.test_sink.state import state


@asynccontextmanager
async def _lifespan(app: FastAPI):
    setup_logging()
    state.verification_key = read_secret(VERIFICATION_KEY_PATH)
    state.control_token = read_secret(CONTROL_TOKEN_PATH)
    state.modes = ModeController()
    state.receipts = ReceiptStore()
    yield


app = FastAPI(lifespan=_lifespan)
app.include_router(routes.router)
# 401 (INVALID_SIGNATURE и пр.) должны приходить клиенту как 401, а не 500.
app.add_exception_handler(ApiError, api_error_handler)


async def main() -> None:
    cfg = uvicorn.Config(
        app,
        host="0.0.0.0",
        port=config.TEST_SINK_PORT,
        log_config=None,
        access_log=False,
    )
    server = uvicorn.Server(cfg)
    await server.serve()