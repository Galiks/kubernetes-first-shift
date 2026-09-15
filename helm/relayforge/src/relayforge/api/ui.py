"""UI-панель RelayForge: отслеживание доставок, кнопка «Начать», тесты,
управление тестовым приёмником и просмотр логов worker.

Страница обслуживается самим API-процессом (без внешних зависимостей/CDN).
Endpoints /ui/api/* не аутентифицируются отдельно: панель предназначена для
локального администрирования (NetPolicy + port-forward оператора); не
выставляйте её наружу (см. SECURITY.md). Control-token для управления
test-sink монтируется только в API-pod (volumes секции deployment-api).
"""

import asyncio
import datetime
import json
import time
import uuid

import httpx
from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse, RedirectResponse, Response
from kubernetes_asyncio.client.exceptions import ApiException as K8sApiException
from prometheus_client import generate_latest

from relayforge import config
from relayforge.api import metrics
from relayforge.api.errors import ApiError
from relayforge.api.routes import perform_create
from relayforge.api.state import state
from relayforge.api.validation import EVENT_TYPE_RE, IDEMPOTENCY_KEY_RE
from relayforge.secrets import read_secret

router = APIRouter()

_DELIVERY_SELECTOR = (
    f"app.kubernetes.io/instance={config.RELEASE},"
    f"app.kubernetes.io/component=delivery"
)

_control_token_cache: bytes | None = None
_control_token_error: str | None = None
_TEST_HISTORY: list[dict] = []


def _control_token() -> bytes:
    global _control_token_cache, _control_token_error
    if _control_token_cache is not None:
        return _control_token_cache
    if _control_token_error:
        raise ApiError(503, "SINK_CONTROL_UNAVAILABLE", _control_token_error)
    try:
        _control_token_cache = read_secret(config.CONTROL_TOKEN_SINK_PATH)
        return _control_token_cache
    except OSError as e:
        _control_token_error = f"control-token not mounted: {e}"
        raise ApiError(503, "SINK_CONTROL_UNAVAILABLE", _control_token_error) from e


async def _sink_request(path: str, *, method: str = "GET", token: bool = False, payload=None):
    headers = {}
    if token:
        headers["Authorization"] = f"Bearer {_control_token().decode()}"
    try:
        async with httpx.AsyncClient(timeout=5.0) as client:
            if method == "POST":
                return await client.post(
                    f"{config.SINK_URL}{path}", headers=headers, json=payload,
                )
            return await client.get(f"{config.SINK_URL}{path}", headers=headers)
    except httpx.HTTPError as e:
        raise ApiError(503, "SINK_UNREACHABLE", f"test-sink недоступен: {e}") from e


@router.get("/")
async def index():
    return RedirectResponse("/ui")


@router.get("/ui")
async def ui_page():
    return Response(content=UI_HTML, media_type="text/html; charset=utf-8")


@router.get("/ui/api/config")
async def ui_config():
    return {
        "release": config.RELEASE,
        "namespace": config.NAMESPACE,
        "destinations": sorted(state.destinations.keys()) if state.destinations else [],
        "max_active_jobs": (
            state.backpressure.max_active if state.backpressure is not None else None
        ),
        "ttl_seconds": config.WORKER_TTL,
        "sink_url": config.SINK_URL,
        "sink_modes": ["normal", "fail-first", "accept-and-drop", "reject", "slow"],
    }


def _metric_line(text: str, name: str) -> float:
    for line in text.splitlines():
        if line.split(" ")[0] == name:
            return float(line.split()[-1])
    return 0.0


def _metric_labeled(text: str, name: str, label: str, value: str) -> float:
    prefix = f'{name}{{"{label}="{value}"'
    for line in text.splitlines():
        if line.startswith(prefix) and line.split(" ")[0].startswith(name + "{"):
            return float(line.split()[-1])
    return 0.0


@router.get("/ui/api/stats")
async def ui_stats():
    text = generate_latest().decode("utf-8")
    return {
        "requests_success": _metric_labeled(text, "relayforge_requests_total", "result", "success"),
        "requests_conflict": _metric_labeled(text, "relayforge_requests_total", "result", "conflict"),
        "requests_backpressure": _metric_labeled(text, "relayforge_requests_total", "result", "backpressure"),
        "requests_invalid": _metric_labeled(text, "relayforge_requests_total", "result", "invalid"),
        "requests_unauthorized": _metric_labeled(text, "relayforge_requests_total", "result", "unauthorized"),
        "jobs_created_total": _metric_line(text, "relayforge_jobs_created_total"),
        "active_jobs": _metric_line(text, "relayforge_active_jobs"),
        "oldest_active_job_seconds": _metric_line(text, "relayforge_oldest_active_job_seconds"),
        "requests_total": sum(
            _metric_labeled(text, "relayforge_requests_total", "result", r)
            for r in ("success", "conflict", "backpressure", "invalid", "unauthorized", "error")
        ),
    }


@router.get("/ui/api/deliveries")
async def ui_deliveries(limit: int = 50):
    if state.k8s is None or state.core is None:
        raise ApiError(503, "UI_UNAVAILABLE", "kubernetes client not initialized")
    jobs = await state.k8s.list_namespaced_job(
        config.NAMESPACE, label_selector=_DELIVERY_SELECTOR, limit=limit,
    )
    pods = await state.core.list_namespaced_pod(
        config.NAMESPACE, label_selector=_DELIVERY_SELECTOR, limit=2000,
    )
    attempts: dict[str, int] = {}
    for pod in pods.items:
        name = (pod.metadata.labels or {}).get("job-name")
        if name:
            attempts[name] = attempts.get(name, 0) + 1

    items = [
        _job_to_delivery(job, attempts.get(job.metadata.name, 0))
        for job in jobs.items
    ]
    items.sort(key=lambda d: d.get("created_at") or "", reverse=True)

    # Состояние приёмника для первых N доставок (попытки/применение).
    async def sink_for(item):
        try:
            item["sink_state"] = await _sink_received(item["id"])
        except ApiError:
            item["sink_state"] = None

    await asyncio.gather(*[sink_for(i) for i in items[:15]])
    return {"deliveries": items, "count": len(items)}


async def _sink_received(delivery_id: str):
    try:
        async with httpx.AsyncClient(timeout=3.0) as client:
            r = await client.get(f"{config.SINK_URL}/received/{delivery_id}")
        if r.status_code == 200:
            return r.json()
    except httpx.HTTPError:
        pass
    return None


def _job_to_delivery(job, attempts: int) -> dict:
    annotations = job.metadata.annotations or {}
    conditions = (job.status.conditions or []) if job.status else []
    complete = next((c for c in conditions if c.type == "Complete" and c.status == "True"), None)
    failed = next((c for c in conditions if c.type == "Failed" and c.status == "True"), None)

    finished_at = None
    if complete or failed:
        cond = complete or failed
        finished_at = (
            cond.last_transition_time.isoformat()
            if cond.last_transition_time
            else (job.status.completion_time.isoformat() if job.status and job.status.completion_time else None)
        )

    status = "queued"
    if complete:
        status = "succeeded"
    elif failed:
        status = "failed"
    elif job.status and job.status.active:
        status = "running"

    retained_until = None
    if finished_at:
        try:
            retained_until = (
                datetime.datetime.fromisoformat(finished_at.replace("Z", "+00:00"))
                + datetime.timedelta(seconds=config.WORKER_TTL)
            ).isoformat()
        except ValueError:
            retained_until = None

    env = None
    try:
        env = job.spec.template.spec.containers[0].env
    except (AttributeError, IndexError):
        pass
    env_map = {e.name: e.value for e in env or [] if e.value is not None}

    return {
        "id": annotations.get("relayforge/delivery-id"),
        "job": job.metadata.name,
        "status": status,
        "attempts": attempts,
        "destination": env_map.get("RELAYFORGE_DESTINATION"),
        "event_type": env_map.get("RELAYFORGE_EVENT_TYPE"),
        "created_at": annotations.get("relayforge/created-at"),
        "finished_at": finished_at,
        "retained_until": retained_until,
        "failure": (
            {"code": "JOB_FAILED", "message": (failed.message or "job failed")}
            if failed
            else None
        ),
    }


async def _delivery_status(delivery_id: str) -> str:
    jobs = await state.k8s.list_namespaced_job(
        config.NAMESPACE, label_selector=f"relayforge/delivery-id={delivery_id}",
    )
    if not jobs.items:
        return "deleted"
    return _job_to_delivery(jobs.items[0], 0)["status"]


@router.post("/ui/api/deliveries")
async def ui_submit_delivery(request: Request):
    """Кнопка «Начать»: принимает доставку через общую логику API."""
    if state.k8s is None:
        raise ApiError(503, "UI_UNAVAILABLE", "kubernetes client not initialized")
    try:
        data = await request.json()
    except Exception as e:
        raise ApiError(400, "INVALID_JSON", f"invalid JSON body: {e}") from e
    if not isinstance(data, dict):
        raise ApiError(422, "INVALID_FIELD", "body must be an object")

    for field in ("destination", "event_type", "payload"):
        if field not in data:
            raise ApiError(422, "MISSING_FIELD", f"missing field: {field}")
    if not isinstance(data["payload"], dict):
        raise ApiError(422, "INVALID_FIELD", "payload must be object")
    if state.destinations and data["destination"] not in state.destinations:
        raise ApiError(422, "UNKNOWN_DESTINATION", f"unknown destination: {data['destination']}")
    if not EVENT_TYPE_RE.match(data["event_type"]):
        raise ApiError(422, "INVALID_EVENT_TYPE", "event_type does not match pattern")

    idem_key = data.get("idempotency_key") or f"ui:{uuid.uuid4().hex}"
    if not IDEMPOTENCY_KEY_RE.match(idem_key):
        raise ApiError(422, "INVALID_IDEMPOTENCY_KEY", "key does not match pattern")

    status_code, payload = await perform_create(idem_key, data)
    return JSONResponse(status_code=status_code, content=payload)


# ---------------------------------------------------------------------------
# Управление тестовым приёмником (test-sink) из UI
# ---------------------------------------------------------------------------


@router.get("/ui/api/sink/state")
async def ui_sink_state():
    r = await _sink_request("/control/state", token=True)
    if r.status_code != 200:
        raise ApiError(502, "SINK_ERROR", f"test-sink вернул {r.status_code}")
    return r.json()


@router.post("/ui/api/sink/mode")
async def ui_sink_mode(request: Request):
    data = await request.json()
    mode = data.get("mode")
    allowed = {"normal", "fail-first", "accept-and-drop", "reject", "slow"}
    if mode not in allowed:
        raise ApiError(422, "INVALID_MODE", f"mode must be one of {sorted(allowed)}")
    n = int(data.get("n", 1) or 1)
    r = await _sink_request("/control/mode", method="POST", token=True,
                            payload={"mode": mode, "n": max(1, n)})
    if r.status_code != 200:
        raise ApiError(502, "SINK_ERROR", f"test-sink вернул {r.status_code}")
    return r.json()


@router.post("/ui/api/sink/reset")
async def ui_sink_reset():
    r = await _sink_request("/control/reset", method="POST", token=True)
    if r.status_code != 200:
        raise ApiError(502, "SINK_ERROR", f"test-sink вернул {r.status_code}")
    return r.json()


# ---------------------------------------------------------------------------
# Тесты (полный сценарий, как helm-test): create → duplicate → terminal → sink
# ---------------------------------------------------------------------------


@router.get("/ui/api/tests")
async def ui_tests_history():
    return {"tests": _TEST_HISTORY}


@router.post("/ui/api/tests/run")
async def ui_run_test():
    if state.k8s is None:
        raise ApiError(503, "UI_UNAVAILABLE", "kubernetes client not initialized")
    if not state.destinations:
        raise ApiError(503, "NO_DESTINATIONS", "destinations not loaded")

    started = time.monotonic()
    destination = "test" if "test" in state.destinations else next(iter(state.destinations))
    key = f"ui:test:{uuid.uuid4().hex}"
    body = {"destination": destination, "event_type": "ui.test", "payload": {"source": "ui"}}
    steps: list[dict] = []
    delivery_id = None

    # 1. create
    try:
        code, payload = await perform_create(key, body)
        delivery_id = payload.get("id")
        steps.append({
            "name": "create",
            "ok": code == 202,
            "detail": f"HTTP {code}, id={delivery_id}, duplicate={payload.get('duplicate')}",
        })
    except ApiError as e:
        steps.append({"name": "create", "ok": False, "detail": f"{e.code}: {e.message}"})
        return _finish_test(steps, None, started)

    # 2. duplicate с тем же ключом
    code2, payload2 = await perform_create(key, body)
    ok_dup = (
        code2 == 200
        and payload2.get("duplicate") is True
        and payload2.get("id") == delivery_id
    )
    steps.append({
        "name": "duplicate (тот же ключ)",
        "ok": ok_dup,
        "detail": f"HTTP {code2}, id={payload2.get('id')}, duplicate={payload2.get('duplicate')}",
    })

    # 3. ожидание terminal
    status = "queued"
    for _ in range(75):
        status = await _delivery_status(delivery_id)
        if status in ("succeeded", "failed"):
            break
        await asyncio.sleep(2)
    steps.append({"name": "wait terminal", "ok": status in ("succeeded", "failed"),
                  "detail": f"status={status}"})

    # 4. проверка приёмника
    sink = await _sink_received(delivery_id)
    ok_sink = bool(sink and sink.get("applied") is True)
    steps.append({"name": "sink received", "ok": ok_sink, "detail": str(sink or {})})

    return _finish_test(steps, delivery_id, started)


def _finish_test(steps: list[dict], delivery_id, started: float) -> dict:
    passed = bool(steps) and all(s["ok"] for s in steps)
    result = {
        "passed": passed,
        "delivery_id": delivery_id,
        "duration_ms": int((time.monotonic() - started) * 1000),
        "steps": steps,
        "finished_at": datetime.datetime.now(datetime.UTC).isoformat(),
    }
    _TEST_HISTORY.insert(0, result)
    del _TEST_HISTORY[20:]
    return result


# ---------------------------------------------------------------------------
# Логи worker-подов доставки
# ---------------------------------------------------------------------------


@router.get("/ui/api/deliveries/{delivery_id}/logs")
async def ui_delivery_logs(delivery_id: str):
    if state.k8s is None or state.core is None:
        raise ApiError(503, "UI_UNAVAILABLE", "kubernetes client not initialized")
    jobs = await state.k8s.list_namespaced_job(
        config.NAMESPACE, label_selector=f"relayforge/delivery-id={delivery_id}",
    )
    if not jobs.items:
        raise ApiError(404, "DELIVERY_NOT_FOUND", "job no longer exists")
    job_name = jobs.items[0].metadata.name

    pods = await state.core.list_namespaced_pod(
        config.NAMESPACE, label_selector=f"job-name={job_name}",
    )
    lines: list[str] = []
    for pod in sorted(
        pods.items,
        key=lambda p: (p.metadata.creation_timestamp or datetime.datetime.now(datetime.UTC)),
    ):
        pod_name = pod.metadata.name
        try:
            log = await state.core.read_namespaced_pod_log(pod_name, config.NAMESPACE)
        except K8sApiException as e:
            log = None
            lines.append(json.dumps({
                "pod": pod_name,
                "level": "ERROR",
                "logger": "relayforge.ui",
                "msg": f"лог недоступен: HTTP {e.status}",
            }, ensure_ascii=False))
        if log is None:
            continue
        # Каждая JSON-строка — один лог-запись с именем пода внутри записи
        # (JSON-lines поток, пригодный для jq/лог-парсеров без внешних заголовков).
        for raw in log.splitlines():
            line = raw.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except ValueError:
                rec = None
            if isinstance(rec, dict):
                lines.append(json.dumps({"pod": pod_name, **rec}, ensure_ascii=False))
            else:
                lines.append(json.dumps(
                    {"pod": pod_name, "level": "RAW", "line": line},
                    ensure_ascii=False,
                ))
    text = "\n".join(lines)
    truncated = len(text) > 200_000
    return {"job": job_name, "logs": text[-200_000:], "truncated": truncated}


UI_HTML = r"""<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>RelayForge — панель доставок</title>
<style>
:root { color-scheme: dark; }
* { box-sizing: border-box; }
body { margin: 0; font: 14px/1.45 system-ui, "Segoe UI", Roboto, sans-serif;
       background: #0f1420; color: #dce3f0; }
header { display: flex; align-items: baseline; gap: 16px; padding: 14px 20px;
         background: #161d2e; border-bottom: 1px solid #27324a; position: sticky; top: 0; z-index: 5; }
header h1 { font-size: 17px; margin: 0; }
header .release { color: #8fb7ff; font-weight: 600; }
header .hint { color: #7d8aa5; font-size: 12px; margin-left: auto; }
.wrap { padding: 18px 20px; display: grid; gap: 16px; max-width: 1500px; margin: 0 auto; }
.card { background: #161d2e; border: 1px solid #27324a; border-radius: 10px; padding: 14px 16px; }
.card h2 { margin: 0 0 10px; font-size: 13px; text-transform: uppercase; letter-spacing: .06em; color: #93a3c0; }
.cols2 { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
@media (max-width: 1100px) { .cols2 { grid-template-columns: 1fr; } }
.stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(120px, 1fr)); gap: 10px; }
.stat { background: #10162a; border: 1px solid #24304a; border-radius: 8px; padding: 8px 12px; }
.stat b { display: block; font-size: 18px; }
.stat span { color: #7d8aa5; font-size: 11px; }
form { display: grid; gap: 10px; }
.row { display: grid; grid-template-columns: 200px 1fr 1fr; gap: 10px; }
.srow { display: flex; gap: 10px; align-items: end; flex-wrap: wrap; }
label { color: #93a3c0; font-size: 12px; display: flex; flex-direction: column; gap: 4px; }
input, select, textarea { background: #0e1426; color: #dce3f0; border: 1px solid #2b3854;
       border-radius: 6px; padding: 8px 10px; font: inherit; }
textarea { resize: vertical; min-height: 84px; font-family: ui-monospace, Menlo, Consolas, monospace; }
button { background: #2c6df0; color: #fff; border: 0; border-radius: 8px; padding: 11px 22px;
         font-size: 15px; font-weight: 600; cursor: pointer; }
button:hover { background: #3f7df6; }
button:disabled { background: #31405f; cursor: wait; }
button.ghost { background: #23304a; }
button.ghost:hover { background: #2d3d5e; }
button.warn { background: #8a2f3a; }
button.warn:hover { background: #a33a48; }
#result, #sink-result { min-height: 22px; font-size: 13px; }
#result.ok, .ok-txt { color: #7ee2a8; }
#result.err, .err-txt { color: #ff9d9d; }
table { width: 100%; border-collapse: collapse; font-size: 13px; }
th, td { text-align: left; padding: 7px 9px; border-bottom: 1px solid #1d2638; vertical-align: top; }
th { color: #7d8aa5; font-weight: 600; position: sticky; top: 57px; background: #161d2e; }
td.mono { font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 12px; }
.badge { display: inline-block; padding: 2px 9px; border-radius: 20px; font-size: 12px; font-weight: 600; }
.badge.queued { background: #233048; color: #9db2d8; }
.badge.running { background: #25436b; color: #8fc6ff; }
.badge.succeeded { background: #123824; color: #7ee2a8; }
.badge.failed { background: #4a1720; color: #ff9d9d; }
.badge-pass { background: #123824; color: #7ee2a8; }
.badge-fail { background: #4a1720; color: #ff9d9d; }
.failure { color: #ff9d9d; font-size: 12px; max-width: 380px; }
.empty { color: #7d8aa5; padding: 14px; text-align: center; }
#refreshing { color: #7d8aa5; font-size: 12px; }
pre.logs { background: #0a101d; border: 1px solid #24304a; border-radius: 8px; padding: 12px;
           max-height: 360px; overflow: auto; font-size: 12px; line-height: 1.5; white-space: pre-wrap; }
.test-step { display: flex; gap: 8px; align-items: baseline; font-size: 13px; padding: 3px 0; }
.test-step .mark { font-weight: 700; }
.test-history-item { border-top: 1px solid #1d2638; padding: 8px 0; }
.toolbar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
dialog { background: #161d2e; color: #dce3f0; border: 1px solid #2b3854; border-radius: 10px; max-width: 900px; width: 92vw; }
dialog::backdrop { background: rgba(5, 8, 16, .7); }
</style>
</head>
<body>
<header>
  <h1>RelayForge — панель доставок</h1>
  <span class="release" id="release-label">…</span>
  <span class="hint" id="refreshing">обновление каждые 3 с</span>
</header>
<div class="wrap">

  <div class="card">
    <h2>Метрики</h2>
    <div class="stats" id="stats"></div>
  </div>

  <div class="cols2">
    <div class="card">
      <h2>Новая доставка</h2>
      <form id="delivery-form" autocomplete="off">
        <div class="row">
          <label>Destination
            <select id="f-destination" name="destination"></select>
          </label>
          <label>Event type
            <input id="f-event" name="event_type" value="ui.test" placeholder="app.event">
          </label>
          <label>Idempotency-Key (пусто = авто)
            <input id="f-key" name="idempotency_key" placeholder="ui:my-key:v1">
          </label>
        </div>
        <label>Payload (JSON)
          <textarea id="f-payload" name="payload" spellcheck="false">{&quot;message&quot;: &quot;hello&quot;}</textarea>
        </label>
        <div class="toolbar">
          <button type="submit" id="start-btn">▶ Начать</button>
          <button type="button" class="ghost" id="run-test-btn">🧪 Запустить тест</button>
          <span id="result"></span>
        </div>
      </form>
      <div id="test-steps" style="margin-top:8px"></div>
    </div>

    <div class="card">
      <h2>Тестовый приёмник (test-sink)</h2>
      <div class="srow">
        <label>Режим
          <select id="sink-mode">
            <option value="normal">normal</option>
            <option value="fail-first">fail-first (N×503)</option>
            <option value="accept-and-drop">accept-and-drop</option>
            <option value="reject">reject (401)</option>
            <option value="slow">slow</option>
          </select>
        </label>
        <label>N (fail-first)
          <input id="sink-n" type="number" min="1" value="1" style="width:90px">
        </label>
        <button type="button" class="ghost" id="sink-apply-btn">Применить</button>
        <button type="button" class="warn" id="sink-reset-btn">Сбросить</button>
        <span id="sink-result"></span>
      </div>
      <div id="sink-state" style="margin-top:8px;color:#93a3c0;font-size:13px"></div>
    </div>
  </div>

  <div id="test-history" class="card"></div>

  <div class="card">
    <h2>Доставки (<span id="count">0</span>)</h2>
    <table>
      <thead>
        <tr>
          <th>Создана</th><th>ID / Job</th><th>Статус</th><th>Destination</th>
          <th>Event</th><th>Попытки</th><th>Sink</th><th>Завершена</th><th>Retained</th><th>Сбой</th><th>Логи</th>
        </tr>
      </thead>
      <tbody id="rows"></tbody>
    </table>
    <div class="empty" id="empty">Доставок пока нет — нажмите «Начать».</div>
  </div>
</div>

<dialog id="logs-dialog">
  <div class="toolbar" style="margin-bottom:8px">
    <b id="logs-title">Логи</b>
    <button type="button" class="ghost" id="logs-close" style="margin-left:auto">Закрыть</button>
  </div>
  <pre class="logs" id="logs-body">…</pre>
</dialog>

<script>
const $ = (id) => document.getElementById(id);
const API = "/ui/api";
let config = null;

function fmt(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  return isNaN(d) ? "—" : d.toLocaleString("ru-RU");
}
function esc(t) {
  const d = document.createElement("div");
  d.textContent = t == null ? "" : String(t);
  return d.innerHTML;
}

async function loadConfig() {
  try {
    config = await (await fetch(API + "/config")).json();
    $("release-label").textContent = config.release + " / " + config.namespace;
    const sel = $("f-destination");
    sel.innerHTML = "";
    config.destinations.forEach((d) => {
      const o = document.createElement("option");
      o.value = d; o.textContent = d; sel.appendChild(o);
    });
  } catch (e) { $("release-label").textContent = "?"; }
  return config;
}

function statChip(label, value) {
  const el = document.createElement("div");
  el.className = "stat";
  const b = document.createElement("b"); b.textContent = value ?? "–";
  const s = document.createElement("span"); s.textContent = label;
  el.append(b, s);
  return el;
}

async function loadStats() {
  try {
    const s = await (await fetch(API + "/stats")).json();
    const box = $("stats"); box.innerHTML = "";
    box.append(
      statChip("Запросов всего", s.requests_total),
      statChip("Успех (202/200)", s.requests_success),
      statChip("Конфликты 409", s.requests_conflict),
      statChip("Backpressure", s.requests_backpressure),
      statChip("Ошибки 4xx", s.requests_invalid),
      statChip("401", s.requests_unauthorized),
      statChip("Jobs создано", s.jobs_created_total),
      statChip("Активных Jobs", s.active_jobs),
      statChip("Старейший активный, с", Math.round(s.oldest_active_job_seconds || 0) + " с"),
    );
  } catch (e) { /* временно недоступно */ }
}

async function loadSinkState() {
  try {
    const s = await (await fetch(API + "/sink/state")).json();
    $("sink-state").textContent =
      "Режим: " + s.mode + (s.mode === "fail-first" ? " (N=" + s.fail_first_n + ")" : "") +
      " · receipts: " + s.receipts_count;
  } catch (e) {
    $("sink-state").textContent = "test-sink недоступен";
  }
}

function badge(status) {
  const span = document.createElement("span");
  span.className = "badge " + status;
  span.textContent = status;
  return span.outerHTML;
}

async function loadDeliveries() {
  try {
    const r = await (await fetch(API + "/deliveries?limit=100")).json();
    $("count").textContent = r.count;
    const tb = $("rows");
    tb.innerHTML = "";
    $("empty").style.display = r.deliveries.length ? "none" : "block";
    r.deliveries.forEach((d) => {
      const tr = document.createElement("tr");
      const c = (v, cls) => { const td = document.createElement("td"); td.className = cls || ""; td.textContent = v ?? "—"; return td; };
      tr.append(c(fmt(d.created_at), "mono"));
      const idTd = document.createElement("td");
      idTd.className = "mono";
      idTd.innerHTML = esc(d.id) + "<br><span style='color:#7d8aa5;font-size:11px'>" + esc(d.job) + "</span>";
      tr.append(idTd);
      const stTd = document.createElement("td"); stTd.innerHTML = badge(d.status); tr.append(stTd);
      tr.append(c(d.destination));
      tr.append(c(d.event_type, "mono"));
      tr.append(c(d.attempts));
      const sTd = document.createElement("td");
      if (d.sink_state) {
        sTd.innerHTML = "attempts: <b>" + d.sink_state.attempts + "</b> · applied: <b>" + d.sink_state.applied + "</b>";
      } else { sTd.textContent = "—"; }
      tr.append(sTd);
      tr.append(c(fmt(d.finished_at), "mono"));
      tr.append(c(fmt(d.retained_until), "mono"));
      const fTd = document.createElement("td");
      if (d.failure) { fTd.className = "failure"; fTd.textContent = (d.failure.message || "").slice(0, 200); }
      tr.append(fTd);
      const lTd = document.createElement("td");
      const btn = document.createElement("button");
      btn.className = "ghost"; btn.textContent = "лог";
      btn.style.padding = "4px 10px"; btn.style.fontSize = "12px";
      btn.onclick = () => showLogs(d.id);
      lTd.appendChild(btn);
      tr.append(lTd);
      tb.appendChild(tr);
    });
  } catch (e) { /* следующее обновление */ }
}

async function showLogs(deliveryId) {
  const dlg = $("logs-dialog");
  $("logs-title").textContent = "Логи worker: " + deliveryId;
  $("logs-body").textContent = "Загрузка…";
  dlg.showModal();
  try {
    const r = await (await fetch(API + "/deliveries/" + encodeURIComponent(deliveryId) + "/logs")).json();
    $("logs-body").textContent = r.logs || "(логов нет)";
  } catch (e) {
    $("logs-body").textContent = "Ошибка: " + e;
  }
}
$("logs-close").onclick = () => $("logs-dialog").close();

async function startDelivery(ev) {
  ev.preventDefault();
  const btn = $("start-btn"), res = $("result");
  btn.disabled = true; res.className = ""; res.textContent = "Отправка…";
  let payload;
  try { payload = JSON.parse($("f-payload").value); }
  catch (e) { res.className = "err"; res.textContent = "Payload — невалидный JSON"; btn.disabled = false; return; }
  const body = {
    destination: $("f-destination").value,
    event_type: $("f-event").value.trim(),
    payload: payload,
  };
  const key = $("f-key").value.trim();
  if (key) body.idempotency_key = key;
  try {
    const r = await fetch(API + "/deliveries", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    const j = await r.json();
    if (r.ok) {
      res.className = "ok";
      res.textContent = "Принято: " + j.id + " (duplicate: " + j.duplicate + ")";
      loadDeliveries(); loadStats();
    } else {
      res.className = "err";
      res.textContent = (j.error && j.error.message) ? (j.error.code + ": " + j.error.message) : ("HTTP " + r.status);
    }
  } catch (e) {
    res.className = "err"; res.textContent = "Ошибка соединения: " + e;
  } finally { btn.disabled = false; }
}

async function runTest() {
  const btn = $("run-test-btn");
  btn.disabled = true; btn.textContent = "Тест идёт…";
  $("test-steps").innerHTML = "";
  try {
    const r = await fetch(API + "/tests/run", { method: "POST" });
    const t = await r.json();
    const box = $("test-steps");
    box.innerHTML = "";
    const head = document.createElement("div");
    head.innerHTML = t.passed
      ? "<span class='ok-txt'><b>ТЕСТ ПРОЙДЕН</b></span>"
      : "<span class='err-txt'><b>ТЕСТ НЕ ПРОЙДЕН</b></span>";
    head.innerHTML += " · " + (t.delivery_id || "—") + " · " + t.duration_ms + " мс";
    box.appendChild(head);
    t.steps.forEach((s) => {
      const div = document.createElement("div");
      div.className = "test-step";
      div.innerHTML = "<span class='mark " + (s.ok ? "ok-txt'>✓" : "err-txt'>✗") + "</span> " +
        esc(s.name) + " — <span style='color:#7d8aa5'>" + esc(s.detail) + "</span>";
      box.appendChild(div);
    });
    loadDeliveries(); loadStats(); loadSinkState(); loadTestHistory();
  } catch (e) {
    $("test-steps").textContent = "Ошибка: " + e;
  } finally {
    btn.disabled = false; btn.textContent = "🧪 Запустить тест";
  }
}

async function loadTestHistory() {
  try {
    const r = await (await fetch(API + "/tests")).json();
    const box = $("test-history");
    if (!r.tests.length) { box.innerHTML = ""; return; }
    box.innerHTML = "<h2>История тестов</h2>";
    r.tests.slice(0, 6).forEach((t) => {
      const div = document.createElement("div");
      div.className = "test-history-item";
      const mark = t.passed ? "badge-pass" : "badge-fail";
      div.innerHTML = "<span class='badge " + mark + "'>" + (t.passed ? "PASS" : "FAIL") + "</span> " +
        " <span class='mono'>" + esc(t.delivery_id || "") + "</span>" +
        " · " + esc(fmt(t.finished_at)) + " · " + t.duration_ms + " мс · " +
        t.steps.map((s) => s.name + (s.ok ? " ✓" : " ✗")).join(" → ");
      box.appendChild(div);
    });
  } catch (e) { /* ignore */ }
}

$("delivery-form").addEventListener("submit", startDelivery);
$("run-test-btn").addEventListener("click", runTest);

$("sink-apply-btn").addEventListener("click", async () => {
  const res = $("sink-result");
  res.className = ""; res.textContent = "Применяю…";
  try {
    const r = await fetch(API + "/sink/mode", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode: $("sink-mode").value, n: parseInt($("sink-n").value, 10) || 1 }),
    });
    const j = await r.json();
    if (r.ok) { res.className = "ok"; res.textContent = "Режим: " + j.mode; }
    else { res.className = "err"; res.textContent = (j.error && j.error.message) || ("HTTP " + r.status); }
    loadSinkState();
  } catch (e) { res.className = "err"; res.textContent = "Ошибка: " + e; }
});

$("sink-reset-btn").addEventListener("click", async () => {
  const res = $("sink-result");
  res.className = ""; res.textContent = "Сброс…";
  try {
    const r = await fetch(API + "/sink/reset", { method: "POST" });
    const j = await r.json();
    res.className = r.ok ? "ok" : "err";
    res.textContent = r.ok ? ("Сброшено: " + JSON.stringify(j)) : ((j.error && j.error.message) || "HTTP " + r.status);
    loadSinkState();
  } catch (e) { res.className = "err"; res.textContent = "Ошибка: " + e; }
});

async function tick() {
  $("refreshing").textContent = "обновление " + new Date().toLocaleTimeString("ru-RU");
  await Promise.all([loadStats(), loadDeliveries(), loadSinkState()]);
}
loadConfig().then(() => { tick(); loadTestHistory(); setInterval(tick, 3000); });
</script>
</body>
</html>
"""