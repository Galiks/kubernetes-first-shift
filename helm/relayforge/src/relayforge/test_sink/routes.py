from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse, Response

from relayforge.test_sink.app import state
from relayforge.test_sink.verifier import verify_request
from relayforge.api.errors import ApiError

router = APIRouter()


@router.post("/events")
async def events(request: Request):
    body = await request.body()
    delivery_id = verify_request(request.headers, body, state.verification_key)

    state.attempts[delivery_id] = state.attempts.get(delivery_id, 0) + 1
    action = state.modes.check_mode(delivery_id)

    if action == "reject":
        return Response(status_code=401)
    if action == "slow":
        import asyncio
        await asyncio.sleep(30)
    if action == "drop":
        # accept-and-drop: применяем, но закрываем соединение
        if delivery_id not in state.applied:
            state.applied.add(delivery_id)
            state.receipts.save(delivery_id)
        import os
        os._exit(1)  # имитация обрыва
    if action == "fail":
        return Response(status_code=503)

    if delivery_id in state.applied:
        return Response(status_code=204)
    state.applied.add(delivery_id)
    state.receipts.save(delivery_id)
    return Response(status_code=204)


@router.get("/received/{delivery_id}")
async def received(delivery_id: str):
    return {
        "applied": delivery_id in state.applied,
        "attempts": state.attempts.get(delivery_id, 0),
    }


async def _verify_control(request: Request):
    auth = request.headers.get("Authorization", "")
    if not auth.startswith("Bearer "):
        raise ApiError(401, "UNAUTHORIZED", "missing control token")
    import hmac
    if not hmac.compare_digest(auth[7:].encode("utf-8"), state.control_token):
        raise ApiError(401, "UNAUTHORIZED", "invalid control token")


@router.post("/control/mode")
async def set_mode(request: Request):
    await _verify_control(request)
    data = await request.json()
    state.modes.set_mode(data["mode"], data.get("n", 1))
    return {"mode": data["mode"]}


@router.post("/control/reset")
async def reset(request: Request):
    await _verify_control(request)
    state.attempts.clear()
    state.applied.clear()
    state.receipts.clear()
    state.modes.reset()
    return {"status": "ok"}