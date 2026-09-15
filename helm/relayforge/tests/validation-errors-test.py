"""Unit-тесты ошибок валидации API (без обращения к Kubernetes)."""

import os

os.environ.setdefault("RELAYFORGE_RELEASE", "relay-test")
os.environ.setdefault("RELAYFORGE_NAMESPACE", "default")

import pytest
from fastapi.testclient import TestClient

from relayforge.api.app import app
from relayforge.api.state import state

AUTH = {"Authorization": "Bearer valid-client-token"}


@pytest.fixture()
def client():
    state.client_token = b"valid-client-token"
    state.destinations = {"test": {"url": "http://relay-test-relayforge-test-sink:8080"}}
    return TestClient(app)


def test_unknown_field_422(client):
    r = client.post(
        "/v1/deliveries",
        headers={**AUTH, "Idempotency-Key": "test:valid:key:1"},
        json={"destination": "test", "event_type": "test.ping", "payload": {}, "bogus": 1},
    )
    assert r.status_code == 422
    assert r.json()["error"]["code"] == "UNKNOWN_FIELDS"


def test_missing_idempotency_key_422(client):
    r = client.post(
        "/v1/deliveries",
        headers=AUTH,
        json={"destination": "test", "event_type": "test.ping", "payload": {}},
    )
    assert r.status_code == 422
    assert r.json()["error"]["code"] == "INVALID_IDEMPOTENCY_KEY"


def test_duplicate_json_keys_400(client):
    r = client.post(
        "/v1/deliveries",
        headers={**AUTH, "Idempotency-Key": "test:valid:key:2"},
        content=b'{"destination":"test","event_type":"a.b.c","payload":{},"payload":{"x":1}}',
    )
    assert r.status_code == 400
    assert r.json()["error"]["code"] == "INVALID_JSON"


def test_body_too_large_413(client):
    big = {"destination": "test", "event_type": "test.ping", "payload": {"pad": "x" * (16 * 1024)}}
    r = client.post(
        "/v1/deliveries",
        headers={**AUTH, "Idempotency-Key": "test:valid:key:3"},
        json=big,
    )
    assert r.status_code == 413
    assert r.json()["error"]["code"] == "BODY_TOO_LARGE"