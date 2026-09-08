import datetime
from starlette.datastructures import Headers

from relayforge.worker.signer import verify
from relayforge.api.errors import ApiError


def verify_request(headers: Headers, body: bytes, key: bytes) -> str:
    delivery_id = headers.get("X-Relay-Id")
    timestamp = headers.get("X-Relay-Timestamp")
    signature = headers.get("X-Relay-Signature")
    if not delivery_id or not timestamp or not signature:
        raise ApiError(401, "MISSING_HEADERS", "required headers missing")

    try:
        ts = datetime.datetime.fromisoformat(timestamp.replace("Z", "+00:00"))
    except ValueError as e:
        raise ApiError(401, "INVALID_TIMESTAMP", str(e)) from e

    now = datetime.datetime.now(datetime.UTC)
    if abs((now - ts).total_seconds()) > 300:
        raise ApiError(401, "CLOCK_SKEW", "timestamp too old or too new")

    if not verify(timestamp, delivery_id, body, signature, key):
        raise ApiError(401, "INVALID_SIGNATURE", "HMAC mismatch")

    return delivery_id