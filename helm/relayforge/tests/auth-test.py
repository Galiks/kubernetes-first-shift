"""Unit-тесты client token: 401 без Kubernetes-вызова.

Требование: при неверном токене API возвращает 401 до обращения к Kubernetes API.
Проверяется тем, что 401 приходит и при state.k8s = None (вызов до K8s).
"""

import os

os.environ.setdefault("RELAYFORGE_RELEASE", "relay-test")
os.environ.setdefault("RELAYFORGE_NAMESPACE", "default")

import pytest
from fastapi.testclient import TestClient

from relayforge.api.app import app
from relayforge.api.state import state


@pytest.fixture()
def client():
    assert state.k8s is None  # гарантия: K8s-клиент не создан
    state.client_token = b"valid-client-token"
    return TestClient(app)


def test_missing_token_401(client):
    r = client.post("/v1/deliveries", json={})
    assert r.status_code == 401
    assert r.json()["error"]["code"] == "UNAUTHORIZED"


def test_wrong_token_401_without_k8s(client):
    r = client.post(
        "/v1/deliveries",
        json={"destination": "test"},
        headers={"Authorization": "Bearer wrong"},
    )
    assert r.status_code == 401
    assert r.json()["error"]["code"] == "UNAUTHORIZED"


def test_malformed_auth_header_401(client):
    r = client.post("/v1/deliveries", json={}, headers={"Authorization": "Basic abc"})
    assert r.status_code == 401