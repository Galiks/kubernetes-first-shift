"""Unit-тесты преобразования Job status в API response (RELAYFORGE_TASK.md).

Используются лёгкие fakes (SimpleNamespace), без живого kubernetes-клиента
и без сети.
"""

import asyncio
import datetime
import os
from types import SimpleNamespace

os.environ.setdefault("RELAYFORGE_RELEASE", "relay-test")
os.environ.setdefault("RELAYFORGE_NAMESPACE", "default")

import pytest

from relayforge import config
from relayforge.api import routes as r
from relayforge.api.state import state


def _cond(c_type, status, message=None):
    return SimpleNamespace(type=c_type, status=status, message=message)


def _status(conditions=None, active=0, completion_time=None, start_time=None):
    return SimpleNamespace(
        conditions=conditions,
        active=active,
        completion_time=completion_time,
        start_time=start_time,
    )


def _job(status=None, annotations=None, uid="job-uid-1"):
    return SimpleNamespace(
        metadata=SimpleNamespace(
            name="relay-a-x",
            uid=uid,
            annotations=annotations or {"relayforge/created-at": "2026-01-01T00:00:00Z"},
        ),
        status=status,
        spec=SimpleNamespace(
            template=SimpleNamespace(
                spec=SimpleNamespace(
                    containers=[
                        SimpleNamespace(
                            env=[
                                SimpleNamespace(value="relay-a-x"),
                                SimpleNamespace(value="test:dest"),
                            ],
                        ),
                    ],
                ),
            ),
        ),
    )


def _terminal_time():
    return datetime.datetime(2026, 5, 1, 12, 0, 0, tzinfo=datetime.timezone.utc)


# --- _job_status mapping ----------------------------------------------------


def test_status_queued_when_no_status():
    assert r._job_status(_job(status=None)) == "queued"


def test_status_queued_when_no_conditions_and_inactive():
    assert r._job_status(_job(status=_status(conditions=[]))) == "queued"


def test_status_queued_when_no_conditions_even_if_active():
    # Реализация: условия отсутствуют => queued (даже при active>0).
    assert r._job_status(_job(status=_status(conditions=[], active=1))) == "queued"


def test_status_running_when_conditions_present_but_not_terminal():
    conds = [_cond("Complete", "False"), _cond("Failed", "False")]
    assert r._job_status(_job(status=_status(conditions=conds, active=2))) == "running"


def test_status_succeeded_on_complete():
    conds = [_cond("Complete", "True")]
    assert r._job_status(_job(status=_status(conditions=conds, active=1))) == "succeeded"


def test_status_failed_on_failed_condition():
    conds = [_cond("Failed", "True")]
    assert r._job_status(_job(status=_status(conditions=conds))) == "failed"


def test_status_failed_wins_over_active():
    conds = [_cond("Complete", "False"), _cond("Failed", "True")]
    assert r._job_status(_job(status=_status(conditions=conds, active=3))) == "failed"


# --- _retained_until --------------------------------------------------------


def test_retained_until_none_when_not_terminal():
    assert r._retained_until(_job(status=_status(conditions=[], active=1))) is None


def test_retained_until_none_when_terminal_but_no_time(monkeypatch):
    conds = [_cond("Complete", "True")]
    assert r._retained_until(_job(status=_status(conditions=conds))) is None


def test_retained_until_from_completion_time(monkeypatch):
    monkeypatch.setattr(config, "WORKER_TTL", 600)
    conds = [_cond("Complete", "True")]
    job = _job(status=_status(conditions=conds, completion_time=_terminal_time()))
    expected = _terminal_time() + datetime.timedelta(seconds=600)
    assert r._retained_until(job) == expected.isoformat()


def test_retained_until_falls_back_to_start_time(monkeypatch):
    monkeypatch.setattr(config, "WORKER_TTL", 60)
    conds = [_cond("Failed", "True")]
    job = _job(status=_status(conditions=conds, start_time=_terminal_time()))
    expected = _terminal_time() + datetime.timedelta(seconds=60)
    assert r._retained_until(job) == expected.isoformat()


# --- _job_failure -----------------------------------------------------------


def test_failure_none_when_no_status():
    assert r._job_failure(_job(status=None)) is None


def test_failure_none_when_succeeded():
    conds = [_cond("Complete", "True")]
    assert r._job_failure(_job(status=_status(conditions=conds))) is None


def test_failure_uses_condition_message():
    conds = [_cond("Failed", "True", message="backoff limit exceeded")]
    f = r._job_failure(_job(status=_status(conditions=conds)))
    assert f == {"code": "JOB_FAILED", "message": "backoff limit exceeded"}


def test_failure_default_message_when_missing():
    conds = [_cond("Failed", "True", message=None)]
    f = r._job_failure(_job(status=_status(conditions=conds)))
    assert f["code"] == "JOB_FAILED"
    assert f["message"] == "job failed"


# --- get_delivery response end-to-end --------------------------------------


async def _fake_verify(request):
    return None


def _pod(kind="Job", uid="job-uid-1"):
    return SimpleNamespace(
        metadata=SimpleNamespace(
            owner_references=[SimpleNamespace(kind=kind, uid=uid)],
        ),
    )


def test_get_delivery_maps_attempts_and_retained_until(monkeypatch):
    monkeypatch.setattr(r, "verify_token", _fake_verify)
    monkeypatch.setattr(config, "WORKER_TTL", 600)

    conds = [_cond("Complete", "True")]
    job = _job(status=_status(conditions=conds, completion_time=_terminal_time()))

    class BatchK8s:
        async def list_namespaced_job(self, namespace, label_selector):
            return SimpleNamespace(items=[job])

    class Core:
        async def list_namespaced_pod(self, namespace, label_selector):
            # 2 своих worker-Pod (ownerUID Job совпадает) + 1 чужой Job + 1 kind=Pod.
            return SimpleNamespace(
                items=[
                    _pod(uid="job-uid-1"),
                    _pod(uid="job-uid-1"),
                    _pod(uid="other-job-uid"),
                    _pod(kind="Pod", uid="job-uid-1"),
                ],
            )

    state.k8s = BatchK8s()
    state.core = Core()
    try:
        resp = asyncio.run(r.get_delivery(None, "relay-a-x"))
    finally:
        state.k8s = None
        state.core = None

    assert resp["id"] == "relay-a-x"
    assert resp["status"] == "succeeded"
    # attempts = только Pods этого Job (по owner UID+kind), а не весь list.
    assert resp["attempts"] == 2
    assert resp["destination"] == "test:dest"
    assert resp["failure"] is None
    expected = _terminal_time() + datetime.timedelta(seconds=600)
    assert resp["retained_until"] == expected.isoformat()
    assert resp["created_at"] == "2026-01-01T00:00:00Z"
    assert resp["finished_at"] == _terminal_time().isoformat()


def test_get_delivery_attempts_all_pods_when_job_uid_missing(monkeypatch):
    monkeypatch.setattr(r, "verify_token", _fake_verify)
    conds = [_cond("Failed", "True")]
    job = _job(status=_status(conditions=conds), uid=None)

    class BatchK8s:
        async def list_namespaced_job(self, namespace, label_selector):
            return SimpleNamespace(items=[job])

    class Core:
        async def list_namespaced_pod(self, namespace, label_selector):
            return SimpleNamespace(items=[_pod(uid="a"), _pod(uid="b"), _pod(uid="c")])

    state.k8s = BatchK8s()
    state.core = Core()
    try:
        resp = asyncio.run(r.get_delivery(None, "relay-a-x"))
    finally:
        state.k8s = None
        state.core = None

    # uid у Job отсутствует => fallback на нефильтрованный список.
    assert resp["attempts"] == 3
    assert resp["status"] == "failed"