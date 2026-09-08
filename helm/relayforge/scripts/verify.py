#!/usr/bin/env python3
import argparse
import asyncio
import os
import sys

import httpx


async def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--release", required=True)
    parser.add_argument("--namespace", required=True)
    args = parser.parse_args()

    client_token_path = os.environ["CLIENT_TOKEN_FILE"]
    control_token_path = os.environ["CONTROL_TOKEN_FILE"]
    client_token = open(client_token_path, "rb").read().strip()
    control_token = open(control_token_path, "rb").read().strip()

    async with httpx.AsyncClient(base_url=args.base_url, timeout=30) as client:
        # 1. /livez
        r = await client.get("/livez")
        assert r.status_code == 200, f"livez: {r.status_code}"

        # 2. /readyz
        r = await client.get("/readyz")
        assert r.status_code == 200, f"readyz: {r.status_code}"

        # 3. /metrics
        r = await client.get("/metrics")
        assert r.status_code == 200
        assert "relayforge_requests_total" in r.text

        # 4. 401 без токена
        r = await client.post("/v1/deliveries", json={})
        assert r.status_code == 401

        # 5. 401 с неверным токеном
        r = await client.post("/v1/deliveries",
                              headers={"Authorization": "Bearer wrong"},
                              json={})
        assert r.status_code == 401

        # 6. Валидация
        r = await client.post("/v1/deliveries",
                              headers={"Authorization": f"Bearer {client_token.decode()}",
                                       "Idempotency-Key": "test:verify:1"},
                              json={"destination": "unknown"})
        assert r.status_code == 422

        # 7. Успешный create
        r = await client.post("/v1/deliveries",
                              headers={"Authorization": f"Bearer {client_token.decode()}",
                                       "Idempotency-Key": "test:verify:2"},
                              json={"destination": "test", "event_type": "test.ping",
                                    "payload": {"x": 1}})
        assert r.status_code == 202, f"create: {r.status_code} {r.text}"
        delivery_id = r.json()["id"]

        # 8. Duplicate
        r = await client.post("/v1/deliveries",
                              headers={"Authorization": f"Bearer {client_token.decode()}",
                                       "Idempotency-Key": "test:verify:2"},
                              json={"destination": "test", "event_type": "test.ping",
                                    "payload": {"x": 1}})
        assert r.status_code == 200
        assert r.json()["duplicate"] is True
        assert r.json()["id"] == delivery_id

        # 9. Conflict
        r = await client.post("/v1/deliveries",
                              headers={"Authorization": f"Bearer {client_token.decode()}",
                                       "Idempotency-Key": "test:verify:2"},
                              json={"destination": "test", "event_type": "test.ping",
                                    "payload": {"x": 2}})
        assert r.status_code == 409

    print("PASS")
    sys.exit(0)


if __name__ == "__main__":
    asyncio.run(main())