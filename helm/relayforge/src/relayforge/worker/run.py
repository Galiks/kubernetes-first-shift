import asyncio
import datetime
import logging
import os
import signal
import sys
import time

import orjson

from relayforge import config
from relayforge.logging import setup_logging
from relayforge.secrets import SIGNING_KEY_PATH, read_secret
from relayforge.worker.classifier import classify
from relayforge.worker.http_client import deliver
from relayforge.worker.signer import sign

logger = logging.getLogger(__name__)

EXIT_SUCCESS = 0
EXIT_TRANSIENT = 11
EXIT_PERMANENT = 12
EXIT_NETWORK = 13


async def run() -> None:
    setup_logging()
    delivery_id = os.environ["RELAYFORGE_DELIVERY_ID"]
    destination = os.environ["RELAYFORGE_DESTINATION"]
    event_type = os.environ["RELAYFORGE_EVENT_TYPE"]
    payload = orjson.loads(os.environ["RELAYFORGE_PAYLOAD"])
    release = os.environ["RELAYFORGE_RELEASE"]
    pod_name = os.environ.get("HOSTNAME", "unknown")

    destinations = config.load_destinations()
    url = config.get_destination_url(destination, destinations) + "/events"
    signing_key = read_secret(SIGNING_KEY_PATH)

    body = orjson.dumps({
        "delivery_id": delivery_id,
        "event_type": event_type,
        "payload": payload,
    }, option=orjson.OPT_SORT_KEYS)
    timestamp = datetime.datetime.now(datetime.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    signature = sign(timestamp, delivery_id, body, signing_key)

    headers = {
        "Content-Type": "application/json",
        "X-Relay-Id": delivery_id,
        "X-Relay-Timestamp": timestamp,
        "X-Relay-Signature": f"v1={signature}",
    }

    loop = asyncio.get_running_loop()
    shutdown = asyncio.Event()
    loop.add_signal_handler(signal.SIGTERM, shutdown.set)

    start = time.monotonic()
    try:
        status, _ = await asyncio.wait_for(
            deliver(url, body, headers),
            timeout=config.WORKER_ACTIVE_DEADLINE - 5,
        )
    except asyncio.TimeoutError:
        # SIGTERM или общий срок Job: wait_for отменяет сетевой вызов,
        # попытка считается неудачной (транзиентная).
        logger.info(
            "request aborted" if shutdown.is_set() else "request timeout",
            extra={
                "release": release,
                "delivery_id": delivery_id,
                "destination": destination,
                "pod": pod_name,
                "shutdown": shutdown.is_set(),
            },
        )
        sys.exit(EXIT_TRANSIENT)
    except Exception as e:
        logger.info("network error", extra={
            "release": release,
            "delivery_id": delivery_id,
            "destination": destination,
            "pod": pod_name,
            "error": str(e)[:200],
        })
        sys.exit(EXIT_NETWORK)

    duration_ms = int((time.monotonic() - start) * 1000)
    logger.info("request completed", extra={
        "release": release,
        "delivery_id": delivery_id,
        "destination": destination,
        "pod": pod_name,
        "http_status": status,
        "duration_ms": duration_ms,
    })

    kind = classify(status)
    if kind == "success":
        sys.exit(EXIT_SUCCESS)
    if kind == "permanent":
        sys.exit(EXIT_PERMANENT)
    sys.exit(EXIT_TRANSIENT)