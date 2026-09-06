import hmac

from fastapi import Request

from relayforge.api.errors import ApiError
from relayforge.api.app import state


async def verify_token(request: Request) -> None:
    auth = request.headers.get("Authorization", "")
    if not auth.startswith("Bearer "):
        raise ApiError(401, "UNAUTHORIZED", "missing or malformed Authorization header")
    provided = auth[7:].encode("utf-8")
    if not hmac.compare_digest(provided, state.client_token):
        raise ApiError(401, "UNAUTHORIZED", "invalid client token")