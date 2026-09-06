from fastapi import APIRouter
from fastapi.responses import JSONResponse

from relayforge.api.app import state

router = APIRouter()


@router.get("/livez")
async def livez():
    return {"status": "ok"}


@router.get("/readyz")
async def readyz():
    if not state.ready:
        return JSONResponse(status_code=503, content={"status": "shutting_down"})
    if not state.destinations:
        return JSONResponse(status_code=503, content={"status": "no_destinations"})
    try:
        await state.k8s.list_namespaced_job(state.k8s.api_client.configuration.host.split("/")[-1] or "default", limit=1)
    except Exception as e:
        return JSONResponse(status_code=503, content={"status": "k8s_unhealthy", "error": str(e)[:100]})
    return {"status": "ok"}