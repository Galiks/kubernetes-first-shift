import os
import sys
import time

import httpx

from relayforge.logging import setup_logging
from relayforge.secrets import CLIENT_TOKEN_PATH, CONTROL_TOKEN_PATH, read_secret

import logging
logger = logging.getLogger(__name__)


def run() -> None:
    setup_logging()
    api_url = os.environ["RELAYFORGE_API_URL"]
    sink_url = os.environ["RELAYFORGE_SINK_URL"]
    client_token = read_secret(CLIENT_TOKEN_PATH).decode("utf-8")
    control_token = read_secret(CONTROL_TOKEN_PATH).decode("utf-8")

    idem_key = f"helm-test:{os.environ.get('HOSTNAME', 'unknown')}:v1"
    body = {
        "destination": os.environ["RELAYFORGE_DESTINATION"],
        "event_type": "test.ping",
        "payload": {"x": 1},
    }
    headers = {
        "Authorization": f"Bearer {client_token}",
        "Idempotency-Key": idem_key,
        "Content-Type": "application/json",
    }

    with httpx.Client(timeout=30) as client:
        # 1. Отправить
        r = client.post(f"{api_url}/v1/deliveries", headers=headers, json=body)
        r.raise_for_status()
        delivery_id = r.json()["id"]
        logger.info("created", extra={"delivery_id": delivery_id})

        # 2. Повтор с тем же ключом
        r = client.post(f"{api_url}/v1/deliveries", headers=headers, json=body)
        assert r.status_code == 200, f"expected 200, got {r.status_code}"
        assert r.json()["duplicate"] is True
        assert r.json()["id"] == delivery_id
        logger.info("duplicate confirmed")

        # 3. Ждать terminal status
        deadline = time.time() + 120
        while time.time() < deadline:
            r = client.get(f"{api_url}/v1/deliveries/{delivery_id}",
                           headers={"Authorization": f"Bearer {client_token}"})
            r.raise_for_status()
            status = r.json()["status"]
            if status in ("succeeded", "failed"):
                break
            time.sleep(1)
        else:
            logger.error("timeout waiting for terminal status")
            sys.exit(1)

        if status != "succeeded":
            logger.error("delivery failed", extra={"status": status})
            sys.exit(1)

        # 4. Проверить test-sink
        r = client.get(f"{sink_url}/received/{delivery_id}",
                       headers={"Authorization": f"Bearer {control_token}"})
        r.raise_for_status()
        data = r.json()
        if not data["applied"]:
            logger.error("not applied in sink")
            sys.exit(1)

    logger.info("PASS")
    sys.exit(0)