"""Unit-тесты HMAC framing (worker.signer) и проверки clock skew (test_sink.verifier)."""

import datetime
import hashlib
import hmac

import pytest
from starlette.datastructures import Headers

from relayforge.api.errors import ApiError
from relayforge.test_sink.verifier import verify_request
from relayforge.worker.signer import sign, verify


KEY = b"test-signing-key-0123456789abcdef"


def _expect(timestamp: str, delivery_id: str, body: bytes) -> str:
    message = f"{timestamp}\n{delivery_id}\n".encode("utf-8") + body
    return hmac.new(KEY, message, hashlib.sha256).hexdigest()


def test_exact_hmac_framing():
    # Фрейминг: timestamp + "\n" + delivery_id + "\n" + body (точные bytes).
    ts = "2026-08-06T12:00:00Z"
    delivery_id = "relay-a-abc123"
    body = b'{"a":1}'
    assert sign(ts, delivery_id, body, KEY) == _expect(ts, delivery_id, body)


def test_verify_roundtrip():
    ts = "2026-08-06T12:00:00Z"
    delivery_id = "relay-a-abc123"
    body = b'{"a":1}'
    signature = f"v1={sign(ts, delivery_id, body, KEY)}"
    assert verify(ts, delivery_id, body, signature, KEY)
    assert not verify(ts, delivery_id, body + b"x", signature, KEY)
    assert not verify(ts, delivery_id, body, "v1=deadbeef", KEY)


def test_verify_accepts_fresh_timestamp():
    ts = datetime.datetime.now(datetime.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    delivery_id = "relay-a-abc123"
    body = b'{"event_type":"invoice.created","payload":{"amount":1}}'
    signature = f"v1={sign(ts, delivery_id, body, KEY)}"
    headers = Headers({
        "X-Relay-Id": delivery_id,
        "X-Relay-Timestamp": ts,
        "X-Relay-Signature": signature,
    })
    assert verify_request(headers, body, KEY) == delivery_id


def test_verify_rejects_clock_skew():
    # Отклонение больше пяти минут.
    ts = (datetime.datetime.now(datetime.UTC) - datetime.timedelta(minutes=10)).strftime(
        "%Y-%m-%dT%H:%M:%SZ"
    )
    delivery_id = "relay-a-abc123"
    body = b"{}"
    signature = f"v1={sign(ts, delivery_id, body, KEY)}"
    headers = Headers({
        "X-Relay-Id": delivery_id,
        "X-Relay-Timestamp": ts,
        "X-Relay-Signature": signature,
    })
    with pytest.raises(ApiError) as ei:
        verify_request(headers, body, KEY)
    assert ei.value.status == 401
    assert ei.value.code == "CLOCK_SKEW"


def test_verify_rejects_bad_signature():
    ts = datetime.datetime.now(datetime.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    headers = Headers({
        "X-Relay-Id": "relay-a-abc123",
        "X-Relay-Timestamp": ts,
        "X-Relay-Signature": "v1=0000000000000000000000000000000000000000000000000000000000000000",
    })
    with pytest.raises(ApiError) as ei:
        verify_request(headers, b"{}", KEY)
    assert ei.value.status == 401
    assert ei.value.code == "INVALID_SIGNATURE"


def test_verify_rejects_missing_headers():
    with pytest.raises(ApiError) as ei:
        verify_request(Headers({}), b"{}", KEY)
    assert ei.value.status == 401
    assert ei.value.code == "MISSING_HEADERS"