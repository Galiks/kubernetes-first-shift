"""Unit-тесты graceful shutdown по SIGTERM для API и worker.

Hermetic: нет сети и живого kubernetes-клиента. Обработчики тестируются
напрямую; асинхронные части (deliver, install_sigterm_handler) подменяются.
"""

import asyncio
import os
import sys
from types import SimpleNamespace

os.environ.setdefault("RELAYFORGE_RELEASE", "relay-test")
os.environ.setdefault("RELAYFORGE_NAMESPACE", "default")

import pytest

from relayforge.api import app as api_app
from relayforge.api.state import state
from relayforge.worker import run as worker_run


# --- API: _request_shutdown --------------------------------------------------


def test_api_shutdown_sets_readiness_false_and_requests_exit(monkeypatch):
    server = SimpleNamespace(should_exit=False)
    monkeypatch.setattr(api_app, "SERVER", server)
    state.ready = True
    api_app._request_shutdown()
    assert state.ready is False
    assert server.should_exit is True


def test_api_shutdown_without_server_is_safe(monkeypatch):
    monkeypatch.setattr(api_app, "SERVER", None)
    state.ready = True
    api_app._request_shutdown()
    assert state.ready is False


# --- worker: install_sigterm_handler -----------------------------------------


class FakeLoop:
    """Захватывает регистрацию обработчика сигнала, не трогая реальный loop."""

    def __init__(self):
        self.registered = []

    def add_signal_handler(self, sig, callback):
        self.registered.append((sig, callback))


def test_worker_sigterm_handler_sets_shutdown_event():
    loop = FakeLoop()
    shutdown = asyncio.Event()
    worker_run.install_sigterm_handler(loop, shutdown)
    (sig, handler), = loop.registered
    assert sig is __import__("signal").SIGTERM
    assert not shutdown.is_set()
    # Вызов обработчика (как сделал бы сигнал) переводит в graceful shutdown.
    handler()
    assert shutdown.is_set()


# --- worker: SIGTERM -> abort -> EXIT_TRANSIENT ------------------------------

def test_worker_shutdown_returns_transient_exit(monkeypatch):
    env = {
        "RELAYFORGE_DELIVERY_ID": "relay-test-x",
        "RELAYFORGE_DESTINATION": "test",
        "RELAYFORGE_EVENT_TYPE": "a.b.c",
        "RELAYFORGE_PAYLOAD": "{}",
        "RELAYFORGE_RELEASE": "relay-test",
        "RELAYFORGE_NAMESPACE": "default",
    }
    for k, v in env.items():
        monkeypatch.setenv(k, v)

    # SIGTERM доставлен сразу после установки обработчика.
    seen = {}

    def fake_install(loop, shutdown):
        seen["shutdown"] = shutdown
        shutdown.set()

    async def hanging_deliver(url, body, headers):
        await asyncio.Event().wait()  # никогда не завершится без отмены

    monkeypatch.setattr(worker_run, "install_sigterm_handler", fake_install)
    monkeypatch.setattr(worker_run, "deliver", hanging_deliver)
    monkeypatch.setattr(worker_run, "read_secret", lambda path: b"test-key")
    monkeypatch.setattr(
        worker_run.config,
        "load_destinations",
        lambda: {"test": {"url": "http://relay-test-test-sink:8080"}},
    )
    # WORKER_ACTIVE_DEADLINE=5 => wait_for timeout = 0 => мгновенная отмена.
    monkeypatch.setattr(worker_run.config, "WORKER_ACTIVE_DEADLINE", 5)

    with pytest.raises(SystemExit) as ei:
        asyncio.run(worker_run.run())

    assert ei.value.code == worker_run.EXIT_TRANSIENT
    assert seen["shutdown"].is_set(), "обработчик SIGTERM должен выставить event"