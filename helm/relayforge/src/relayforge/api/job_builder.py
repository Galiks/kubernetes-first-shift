import datetime
import hashlib
import secrets

from relayforge import config
from kubernetes_asyncio import client as k8s

COMPONENT_DELIVERY = "delivery"

def job_name(release: str, idempotency_key: str) -> str:
    h = hashlib.sha256(idempotency_key.encode("utf-8")).hexdigest()
    prefix = f"{release}-"
    return (prefix + h)[:63].rstrip("-")


def new_delivery_id(release: str) -> str:
    return f"{release}-{secrets.token_hex(8)}"


def build_job(
    *,
    release: str,
    namespace: str,
    name: str,
    delivery_id: str,
    canonical_bytes: bytes,
    idem_key_hash: str,
    destination: str,
    event_type: str,
    payload_json: str,
    image: str,
    image_digest: str,
    signing_secret_name: str,
    signing_secret_key: str,
    destinations_configmap: str,
    worker_service_account: str,
    backoff_limit: int,
    active_deadline: int,
    ttl_after_finished: int,
    permanent_exit_code: int,
    secret_revision: str,
) -> k8s.V1Job:
    request_hash = hashlib.sha256(canonical_bytes).hexdigest()
    now = datetime.datetime.now(datetime.UTC).isoformat()

    return k8s.V1Job(
        api_version="batch/v1",
        kind="Job",
        metadata=k8s.V1ObjectMeta(
            name=name,
            namespace=namespace,
            labels={
                "app.kubernetes.io/name": config.APP_NAME,
                "app.kubernetes.io/instance": release,
                "app.kubernetes.io/component": COMPONENT_DELIVERY,
                "relayforge/delivery-id": delivery_id,
            },
            annotations={
                "relayforge/idempotency-key-hash": idem_key_hash,
                "relayforge/request-hash": request_hash,
                "relayforge/delivery-id": delivery_id,
                "relayforge/created-at": now,
                "relayforge/format-version": "1",
                "relayforge/secret-revision": secret_revision,
            },
        ),
        spec=k8s.V1JobSpec(
            backoff_limit=backoff_limit,
            active_deadline_seconds=active_deadline,
            ttl_seconds_after_finished=ttl_after_finished,
            pod_failure_policy=k8s.V1PodFailurePolicy(
                rules=[
                    k8s.V1PodFailurePolicyRule(
                        action="FailJob",
                        on_exit_codes=k8s.V1PodFailurePolicyOnExitCodesRequirement(
                            container_name="worker",
                            operator="In",
                            values=[permanent_exit_code],
                        ),
                    ),
                ],
            ),
            template=k8s.V1PodTemplateSpec(
                metadata=k8s.V1ObjectMeta(
                    labels={
                        "app.kubernetes.io/name": config.APP_NAME,
                        "app.kubernetes.io/instance": release,
                        "app.kubernetes.io/component": COMPONENT_DELIVERY,
                        "relayforge/delivery-id": delivery_id,
                    },
                    # Новая revision секрета должна попадать в Pod template
                    # следующих delivery Jobs (ротация секретов).
                    annotations={
                        "relayforge/secret-revision": secret_revision,
                    },
                ),
                spec=k8s.V1PodSpec(
                    service_account_name=worker_service_account,
                    automount_service_account_token=False,
                    restart_policy="Never",
                    security_context=k8s.V1PodSecurityContext(
                        run_as_non_root=True,
                        run_as_user=1000,
                        fs_group=1000,
                        seccomp_profile=k8s.V1SeccompProfile(type="RuntimeDefault"),
                    ),
                    containers=[
                        k8s.V1Container(
                            name="worker",
                            image=f"{image}@{image_digest}",
                            image_pull_policy="IfNotPresent",
                            command=["python", "-m", "relayforge", "worker"],
                            env=[
                                k8s.V1EnvVar(name="RELAYFORGE_DELIVERY_ID", value=delivery_id),
                                k8s.V1EnvVar(name="RELAYFORGE_DESTINATION", value=destination),
                                k8s.V1EnvVar(name="RELAYFORGE_EVENT_TYPE", value=event_type),
                                k8s.V1EnvVar(name="RELAYFORGE_PAYLOAD", value=payload_json),
                                k8s.V1EnvVar(name="RELAYFORGE_RELEASE", value=release),
                                k8s.V1EnvVar(name="RELAYFORGE_NAMESPACE", value=namespace),
                            ],
                            resources=k8s.V1ResourceRequirements(
                                requests={"cpu": "50m", "memory": "64Mi"},
                                limits={"cpu": "200m", "memory": "128Mi"},
                            ),
                            security_context=k8s.V1SecurityContext(
                                allow_privilege_escalation=False,
                                read_only_root_filesystem=True,
                                capabilities=k8s.V1Capabilities(drop=["ALL"]),
                            ),
                            volume_mounts=[
                                k8s.V1VolumeMount(
                                    name="config",
                                    mount_path="/etc/relayforge/config",
                                    read_only=True,
                                ),
                                k8s.V1VolumeMount(
                                    name="secrets",
                                    mount_path="/etc/relayforge/secrets",
                                    read_only=True,
                                ),
                            ],
                        ),
                    ],
                    volumes=[
                        k8s.V1Volume(
                            name="config",
                            config_map=k8s.V1ConfigMapVolumeSource(
                                name=destinations_configmap,
                            ),
                        ),
                        k8s.V1Volume(
                            name="secrets",
                            secret=k8s.V1SecretVolumeSource(
                                secret_name=signing_secret_name,
                                items=[
                                    k8s.V1KeyToPath(key=signing_secret_key, path="signing-key"),
                                ],
                            ),
                        ),
                    ],
                ),
            ),
        ),
    )