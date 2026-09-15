"""Unit-тесты UI-панели (страница /ui и endpoints /ui/api/*)."""

import json
import os
from types import SimpleNamespace

os.environ.setdefault("RELAYFORGE_RELEASE", "relay-test")
os.environ.setdefault("RELAYFORGE_NAMESPACE", "default")

import pytest
from fastapi.testclient import TestClient
from kubernetes_asyncio.client.exceptions import ApiException

from relayforge.api.app import app
from relayforge.api.state import state


class _FakeCore:
    """CoreV1Api-фейк: список подов + логи по имени (None -> 404)."""

    def __init__(self, logs):
        self._logs = logs

    async def list_namespaced_pod(self, namespace, label_selector=None):
        return SimpleNamespace(items=[
            SimpleNamespace(metadata=SimpleNamespace(name=name, creation_timestamp=None))
            for name in self._logs
        ])

    async def read_namespaced_pod_log(self, name, namespace):
        if self._logs.get(name) is None:
            raise ApiException(status=404, reason="Not Found")
        return self._logs[name]


class _FakeK8s:
    """BatchV1Api-фейк: один Job на любой delivery."""

    async def list_namespaced_job(self, namespace, label_selector=None):
        return SimpleNamespace(items=[SimpleNamespace(metadata=SimpleNamespace(name="job-x"))])


@pytest.fixture()
def client():
    state.destinations = {"test": {"url": "http://sink:8080"}}
    return TestClient(app)


def test_ui_page_served(client):
    r = client.get("/ui")
    assert r.status_code == 200
    assert "text/html" in r.headers["content-type"]
    assert "Начать" in r.text
    assert "RelayForge" in r.text


def test_root_redirects_to_ui(client):
    r = client.get("/", follow_redirects=False)
    assert r.status_code in (302, 307)
    assert r.headers["location"] == "/ui"


def test_ui_config(client):
    r = client.get("/ui/api/config")
    assert r.status_code == 200
    data = r.json()
    assert data["release"] == "relay-test"
    assert data["destinations"] == ["test"]


def test_ui_list_requires_k8s(client):
    r = client.get("/ui/api/deliveries")
    assert r.status_code == 503
    assert r.json()["error"]["code"] == "UI_UNAVAILABLE"


def test_ui_create_requires_k8s(client):
    r = client.post("/ui/api/deliveries", json={})
    assert r.status_code == 503
    assert r.json()["error"]["code"] == "UI_UNAVAILABLE"


def test_ui_stats_shape(client):
    r = client.get("/ui/api/stats")
    assert r.status_code == 200
    data = r.json()
    for key in ("requests_total", "active_jobs", "jobs_created_total"):
        assert key in data

def test_ui_config_extended(client):
    data = client.get("/ui/api/config").json()
    assert "sink_url" in data
    assert data["sink_modes"] == ["normal", "fail-first", "accept-and-drop", "reject", "slow"]


def test_ui_sink_state_without_control_token(client):
    # control-token не смонтирован в тестовом окружении -> 503
    r = client.get("/ui/api/sink/state")
    assert r.status_code == 503
    assert r.json()["error"]["code"] in ("SINK_CONTROL_UNAVAILABLE", "SINK_UNREACHABLE")


def test_ui_sink_mode_invalid(client):
    r = client.post("/ui/api/sink/mode", json={"mode": "unknown"})
    assert r.status_code == 422
    assert r.json()["error"]["code"] == "INVALID_MODE"


def test_ui_tests_require_k8s(client):
    r = client.post("/ui/api/tests/run")
    assert r.status_code == 503
    assert r.json()["error"]["code"] == "UI_UNAVAILABLE"


def test_ui_tests_history_empty(client):
    r = client.get("/ui/api/tests")
    assert r.status_code == 200
    assert r.json()["tests"] == []


def test_ui_logs_require_k8s(client):
    r = client.get("/ui/api/deliveries/some-id/logs")
    assert r.status_code == 503
    assert r.json()["error"]["code"] == "UI_UNAVAILABLE"


def test_ui_logs_pod_name_inside_json(client, monkeypatch):
    logs = {
        "relay-a-abc123xyz": (
            '{"ts":"2026-09-14T00:00:00Z","level":"INFO","logger":"httpx",'
            '"msg":"HTTP Request: POST http://sink:8080/events \\"HTTP/1.1 503 Service Unavailable\\""}\n'
            "plain text line\n"
        ),
        "relay-a-def456uvw": None,  # лог недоступен -> запись ERROR с pod
    }
    monkeypatch.setattr(state, "k8s", _FakeK8s())
    monkeypatch.setattr(state, "core", _FakeCore(logs))
    r = client.get("/ui/api/deliveries/d1/logs")
    assert r.status_code == 200
    body = r.json()
    assert body["job"] == "job-x"
    records = [json.loads(line) for line in body["logs"].splitlines()]
    assert len(records) == 3
    # Каждая запись содержит имя пода ВНУТРИ JSON, без заголовков `--- pod ---`.
    assert records[0]["pod"] == "relay-a-abc123xyz"
    assert records[0]["logger"] == "httpx"
    assert "503 Service Unavailable" in records[0]["msg"]
    assert records[1]["pod"] == "relay-a-abc123xyz"
    assert records[1]["level"] == "RAW"
    assert records[1]["line"] == "plain text line"
    assert records[2]["pod"] == "relay-a-def456uvw"
    assert records[2]["level"] == "ERROR"
    assert "404" in records[2]["msg"]
    assert "---" not in body["logs"]


def test_ui_logs_missing_delivery_404(client, monkeypatch):
    class _EmptyK8s:
        async def list_namespaced_job(self, namespace, label_selector=None):
            return SimpleNamespace(items=[])

    monkeypatch.setattr(state, "k8s", _EmptyK8s())
    monkeypatch.setattr(state, "core", _FakeCore({}))
    r = client.get("/ui/api/deliveries/ghost/logs")
    assert r.status_code == 404
    assert r.json()["error"]["code"] == "DELIVERY_NOT_FOUND"
