import asyncio
import os
import sys

from kubernetes_asyncio import client, config

from relayforge.logging import setup_logging

import logging
logger = logging.getLogger(__name__)


async def run() -> None:
    setup_logging()
    config.load_incluster_config()  # синхронная функция
    apps = client.AppsV1Api()
    batch = client.BatchV1Api()

    release = os.environ["RELAYFORGE_RELEASE"]
    namespace = os.environ["RELAYFORGE_NAMESPACE"]
    api_name = f"{release}-relayforge-api"

    # 1. Scale API в 0
    logger.info("scaling api to 0")
    await apps.patch_namespaced_deployment_scale(
        api_name, namespace,
        body={"spec": {"replicas": 0}},
    )

    # 2. Ждать status.replicas == 0
    for _ in range(60):
        dep = await apps.read_namespaced_deployment(api_name, namespace)
        if (dep.status.replicas or 0) == 0 and (dep.status.available_replicas or 0) == 0:
            break
        await asyncio.sleep(1)
    else:
        logger.error("timeout waiting for api scale-down")
        sys.exit(1)

    # 3. Удалить Jobs своего release с component=delivery
    jobs = await batch.list_namespaced_job(
        namespace,
        label_selector=f"app.kubernetes.io/instance={release},app.kubernetes.io/component=delivery",
    )
    for job in jobs.items:
        logger.info("deleting job", extra={"job": job.metadata.name})
        await batch.delete_namespaced_job(
            job.metadata.name, namespace,
            propagation_policy="Background",
        )

    logger.info("cleanup done")
    sys.exit(0)