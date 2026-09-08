import pytest


@pytest.fixture
def sample_request():
    return {
        "destination": "test",
        "event_type": "test.ping",
        "payload": {"x": 1},
    }


@pytest.fixture
def signing_key():
    return b"test-signing-key-0123456789abcdef"