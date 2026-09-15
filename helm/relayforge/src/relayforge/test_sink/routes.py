from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse, Response

from relayforge.api.errors import ApiError
from relayforge.test_sink.state import state
from relayforge.test_sink.verifier import verify_request

router = APIRouter()


@router.get("/livez")
async def livez():
    # Probe для test-sink: только проверка HTTP-процесса.
    return {"status": "ok"}


@router.post("/events")
async def events(request: Request):
    body = await request.body()
    delivery_id = verify_request(request.headers, body, state.verification_key)

    # HTTP-попытки считаются отдельно от применённых событий и персистятся
    # на emptyDir: счётчик переживает restart контейнера (сценарий 07/10).
    attempts = state.receipts.record_attempt(delivery_id)
    action = state.modes.check_mode(delivery_id)

    if action == "reject":
        return Response(status_code=401)
    if action == "slow":
        import asyncio
        await asyncio.sleep(30)
    if action == "drop":
        # accept-and-drop: применяем, но закрываем соединение без ответа
        state.receipts.apply(delivery_id)
        import os
        os._exit(1)  # имитация обрыва
    if action == "fail":
        return Response(status_code=503)

    if state.receipts.is_applied(delivery_id):
        # Уже применённое событие: 204, повторно не применяем.
        return Response(status_code=204)
    state.receipts.apply(delivery_id)
    return Response(status_code=204)


@router.get("/received/{delivery_id}")
async def received(delivery_id: str):
    return {
        "applied": state.receipts.is_applied(delivery_id),
        "attempts": state.receipts.attempts(delivery_id),
    }


async def _verify_control(request: Request):
    auth = request.headers.get("Authorization", "")
    if not auth.startswith("Bearer "):
        raise ApiError(401, "UNAUTHORIZED", "missing control token")
    import hmac
    if not hmac.compare_digest(auth[7:].encode("utf-8"), state.control_token):
        raise ApiError(401, "UNAUTHORIZED", "invalid control token")


@router.get("/control/state")
async def control_state(request: Request):
    await _verify_control(request)
    return {
        "mode": state.modes.mode,
        "fail_first_n": state.modes.fail_first_n,
        "receipts_count": state.receipts.count(),
    }


@router.post("/control/mode")
async def set_mode(request: Request):
    await _verify_control(request)
    data = await request.json()
    state.modes.set_mode(data["mode"], data.get("n", 1))
    return {"mode": data["mode"]}


@router.post("/control/reset")
async def reset(request: Request):
    await _verify_control(request)
    state.receipts.clear()
    state.modes.reset()
    return {"status": "ok"}