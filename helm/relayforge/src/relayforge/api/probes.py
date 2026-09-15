from fastapi import APIRouter
from fastapi.responses import JSONResponse

from relayforge import config
from relayforge.api.state import state

router = APIRouter()


@router.get("/livez")
async def livez():
    # Liveness проверяет только HTTP-процесс и не обращается к Kubernetes API.
    return {"status": "ok"}


@router.get("/readyz")
async def readyz():
    if not state.ready:
        return JSONResponse(status_code=503, content={"status": "shutting_down"})
    if not state.destinations:
        return JSONResponse(status_code=503, content={"status": "no_destinations"})
    if state.k8s is None:
        return JSONResponse(status_code=503, content={"status": "k8s_uninitialized"})
    try:
        await state.k8s.list_namespaced_job(config.NAMESPACE, limit=1)
    except Exception as e:
        return JSONResponse(
            status_code=503,
            content={"status": "k8s_unhealthy", "error": str(e)[:100]},
        )
    return {"status": "ok"}