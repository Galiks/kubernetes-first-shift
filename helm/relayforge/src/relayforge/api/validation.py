import re

from fastapi import Request

from relayforge.api.app import state
from relayforge.api.errors import ApiError
from relayforge.canonical import strict_json_loads

IDEMPOTENCY_KEY_RE = re.compile(r"^[A-Za-z0-9._:-]{8,128}$")
EVENT_TYPE_RE = re.compile(r"^[a-z][a-z0-9_.-]{2,63}$")
MAX_BODY = 16 * 1024


async def validate_request(request: Request) -> tuple[str, dict, bytes]:
    # 1. Размер
    length = request.headers.get("content-length")
    if length and int(length) > MAX_BODY:
        raise ApiError(413, "BODY_TOO_LARGE", "body exceeds 16 KiB")

    # 2. Чтение + UTF-8
    raw = await request.body()
    if len(raw) > MAX_BODY:
        raise ApiError(413, "BODY_TOO_LARGE", "body exceeds 16 KiB")
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError as e:
        raise ApiError(400, "INVALID_UTF8", str(e)) from e

    # 3. Строгий JSON
    try:
        data = strict_json_loads(text)
    except ValueError as e:
        raise ApiError(400, "INVALID_JSON", str(e)) from e

    # 4. Неизвестные поля
    allowed = {"destination", "event_type", "payload"}
    unknown = set(data) - allowed
    if unknown:
        raise ApiError(422, "UNKNOWN_FIELDS", f"unknown fields: {sorted(unknown)}")

    # 5. Обязательные поля
    for field in allowed:
        if field not in data:
            raise ApiError(422, "MISSING_FIELD", f"missing field: {field}")

    # 6. Типы
    if not isinstance(data["destination"], str):
        raise ApiError(422, "INVALID_FIELD", "destination must be string")
    if not isinstance(data["event_type"], str):
        raise ApiError(422, "INVALID_FIELD", "event_type must be string")
    if not isinstance(data["payload"], dict):
        raise ApiError(422, "INVALID_FIELD", "payload must be object")

    # 7. Destination из конфига
    if data["destination"] not in state.destinations:
        raise ApiError(422, "UNKNOWN_DESTINATION", f"unknown destination: {data['destination']}")

    # 8. Regex
    if not EVENT_TYPE_RE.match(data["event_type"]):
        raise ApiError(422, "INVALID_EVENT_TYPE", "event_type does not match pattern")

    # 9. Idempotency-Key
    idem_key = request.headers.get("Idempotency-Key", "")
    if not IDEMPOTENCY_KEY_RE.match(idem_key):
        raise ApiError(422, "INVALID_IDEMPOTENCY_KEY", "key does not match pattern")

    return idem_key, data, raw