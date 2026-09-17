"""Unit-тесты: стабильное имя Job по ключу + новый delivery ID после
пересоздания Job (RELAYFORGE_TASK.md).

Hermetic: нет kubernetes-кластера; используются реальные объекты kubernetes_asyncio
(конструкторы моделей, без сети).
"""

import os

os.environ.setdefault("RELAYFORGE_RELEASE", "relay-test")
os.environ.setdefault("RELAYFORGE_NAMESPACE", "default")

from relayforge.api.job_builder import build_job, job_name, new_delivery_id


def _build(name, delivery_id, secret_revision="7"):
    return build_job(
        release="relay-test",
        namespace="default",
        name=name,
        delivery_id=delivery_id,
        canonical_bytes=b"{canonical}",
        idem_key_hash="idem-hash",
        destination="test",
        event_type="a.b.c",
        payload_json="{}",
        image="localhost:5050/relayforge",
        image_digest="sha256:abc",
        signing_secret_name="signing",
        signing_secret_key="signing-key",
        destinations_configmap="cfg",
        worker_service_account="sa",
        backoff_limit=1,
        active_deadline=100,
        ttl_after_finished=600,
        permanent_exit_code=12,
        secret_revision=secret_revision,
    )


def test_job_name_stable_for_same_key():
    assert job_name("relay-test", "order:key:1") == job_name("relay-test", "order:key:1")


def test_job_name_differs_for_other_key():
    assert job_name("relay-test", "order:key:1") != job_name("relay-test", "order:key:2")


def test_job_name_is_deterministic_and_bounded():
    name = job_name("relay-test", "x" * 128)
    assert len(name) <= 63


def test_recreated_job_reuses_name_but_new_delivery_id():
    name = job_name("relay-test", "order:key:1")
    id1 = new_delivery_id("relay-test")
    id2 = new_delivery_id("relay-test")

    j1 = _build(name, id1)
    j2 = _build(name, id2)

    # После TTL-удаления детерминированное ИМЯ сохраняется...
    assert j1.metadata.name == name
    assert j2.metadata.name == name
    # ...но delivery ID — новый (annotations несут новый id).
    assert j1.metadata.annotations["relayforge/delivery-id"] == id1
    assert j2.metadata.annotations["relayforge/delivery-id"] == id2
    assert id1 != id2


def test_recreated_job_carries_new_id_in_pod_template():
    name = job_name("relay-test", "order:key:9")
    j1 = _build(name, new_delivery_id("relay-test"))
    j2 = _build(name, new_delivery_id("relay-test"))

    pod_labels1 = j1.spec.template.metadata.labels
    pod_labels2 = j2.spec.template.metadata.labels
    assert pod_labels1["relayforge/delivery-id"] == j1.metadata.annotations["relayforge/delivery-id"]
    assert pod_labels2["relayforge/delivery-id"] == j2.metadata.annotations["relayforge/delivery-id"]
    assert pod_labels1["relayforge/delivery-id"] != pod_labels2["relayforge/delivery-id"]


def test_secret_revision_annotation_on_job_and_pod_template():
    name = job_name("relay-test", "order:key:9")
    job = _build(name, new_delivery_id("relay-test"), secret_revision="42")
    # Аннотация revision секрета есть и на самом Job...
    assert job.metadata.annotations["relayforge/secret-revision"] == "42"
    # ...и на Pod template (чтобы Pod worker нёс текущую revision).
    assert job.spec.template.metadata.annotations["relayforge/secret-revision"] == "42"