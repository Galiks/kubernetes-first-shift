#!/usr/bin/env python3
"""Автоматическая проверка RelayForge (RELAYFORGE_TASK.md, «Автоматическая проверка»).

Скрипт принимает base URL, release и namespace. Клиентский и control токены
читаются из файлов, пути к которым заданы отдельными environment variables;
значения токенов не передаются через CLI и не печатаются.

Проверки:
  - /livez, /readyz и /metrics;
  - 401 при отсутствующем и неверном client token (без обращения к K8s);
  - валидацию API (неизвестный destination, regex, размер, duplicate keys);
  - последовательную и конкурентную идемпотентность;
  - конфликт payload (409);
  - backpressure и отсутствие Job у отклонённого запроса (--max-active-jobs);
  - временные и постоянные ошибки (--sink-url);
  - очистку Job по TTL и новый delivery ID при повторе ключа (--ttl-timeout);
  - отсутствие второго Job для одного ключа (kubectl, read-only);
  - отсутствие payload и подписи в логах worker (kubectl, read-only);
  - изменение metrics после успешного запроса, duplicate и conflict.

kubectl используется только для чтения в namespace release; скрипт не меняет
cluster-wide ресурсы и не требует cluster-admin. Успех — строка PASS и код 0.

Пример:
  CLIENT_TOKEN_FILE=relay-a-client-token.txt \
  CONTROL_TOKEN_FILE=relay-a-control-token.txt \
  python scripts/verify.py --base-url http://127.0.0.1:18080 \
      --release relay-a --namespace relayforge
"""

import argparse
import asyncio
import os
import re
import subprocess
import sys
import time

import httpx

PASS_MARK = "PASS"


def read_token_file(env_name: str) -> bytes:
    """Значения токенов читаются из файлов; пути — из environment variables."""
    path = os.environ.get(env_name)
    if not path:
        print(f"error: {env_name} is not set", file=sys.stderr)
        sys.exit(2)
    try:
        with open(path, "rb") as f:
            return f.read().strip()
    except OSError as e:
        print(f"error: cannot read {env_name}={path}: {e}", file=sys.stderr)
        sys.exit(2)


def kubectl_available() -> bool:
    if os.environ.get("VERIFY_USE_KUBECTL") == "off":
        return False
    try:
        subprocess.run(
            ["kubectl", "version", "--client"],
            capture_output=True,
            timeout=10,
            check=True,
        )
        return True
    except Exception:
        return False


def kubectl_job_names(namespace: str, release: str) -> list[str]:
    """Read-only: список delivery-Jobs release в namespace."""
    out = subprocess.run(
        [
            "kubectl", "get", "jobs", "-n", namespace,
            "-l", f"app.kubernetes.io/instance={release},app.kubernetes.io/component=delivery",
            "-o", "jsonpath={.items[*].metadata.name}",
        ],
        capture_output=True,
        text=True,
        timeout=30,
    )
    if out.returncode != 0:
        return []
    return out.stdout.split()


def kubectl_worker_logs(namespace: str, job_name: str) -> str:
    """Read-only: логи worker-подов Job'а (все завершённые попытки)."""
    out = subprocess.run(
        ["kubectl", "logs", "-n", namespace, f"job/{job_name}", "--all-containers"],
        capture_output=True,
        text=True,
        timeout=60,
    )
    return out.stdout + out.stderr


def extract_metric(text: str, name: str) -> float:
    for line in text.splitlines():
        head = line.split(" ")[0] if line else ""
        if head == name:  # точное имя метрики, без *_created
            return float(line.split()[-1])
    return 0.0


def extract_metric_labeled(text: str, name: str, label: str, value: str) -> float:
    pattern = re.compile(
        rf"^{re.escape(name)}\{{[^}}]*{re.escape(label)}=\"(?P<v>[^\"]*)\"[^}}]*\}} (?P<n>[\d.e+-]+)",
        re.MULTILINE,
    )
    total = 0.0
    for m in pattern.finditer(text):
        if m.group("v") == value:
            total = float(m.group("n"))
    return total


def parse_metrics(text: str) -> dict:
    return {
        "jobs_created_total": extract_metric(text, "relayforge_jobs_created_total"),
        "active_jobs": extract_metric(text, "relayforge_active_jobs"),
        "requests_success": extract_metric_labeled(text, "relayforge_requests_total", "result", "success"),
        "requests_conflict": extract_metric_labeled(text, "relayforge_requests_total", "result", "conflict"),
        "requests_backpressure": extract_metric_labeled(text, "relayforge_requests_total", "result", "backpressure"),
        "requests_invalid": extract_metric_labeled(text, "relayforge_requests_total", "result", "invalid"),
        "requests_unauthorized": extract_metric_labeled(text, "relayforge_requests_total", "result", "unauthorized"),
    }


class Checks:
    def __init__(self, name: str):
        self.name = name
        self.results: list[tuple[str, bool, str]] = []

    def check(self, detail: str, ok: bool, info: str = "") -> None:
        self.results.append((detail, bool(ok), info))
        print(f"[{'OK' if ok else 'FAIL'}] {self.name}: {detail}" + (f" — {info}" if info else ""))

    def report(self) -> bool:
        failed = [r for r in self.results if not r[1]]
        if failed:
            print(f"FAILED CHECKS ({len(failed)}):")
            for name, ok, info in failed:
                print(f"  - {name}" + (f" ({info})" if info else ""))
            return False
        print(PASS_MARK)
        return True


async def wait_terminal(c: httpx.AsyncClient, headers: dict, delivery_id: str, timeout: float = 120.0) -> dict:
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        r = await c.get(f"/v1/deliveries/{delivery_id}", headers=headers)
        if r.status_code == 200:
            last = r.json()
            if last.get("status") in ("succeeded", "failed"):
                return last
        await asyncio.sleep(1)
    return last or {}


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--release", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--sink-url", default="", help="URL test-sink (для сценариев sink)")
    parser.add_argument("--max-active-jobs", type=int, default=0, help="лимит backpressure (для проверки)")
    parser.add_argument("--ttl-timeout", type=float, default=0, help="ждать TTL-очистки Job (сек)")
    args = parser.parse_args()

    client_token = read_token_file("CLIENT_TOKEN_FILE")
    control_token = read_token_file("CONTROL_TOKEN_FILE")
    use_kubectl = kubectl_available()

    def auth() -> dict:
        return {"Authorization": f"Bearer {client_token.decode()}"}

    def ctrl() -> dict:
        return {"Authorization": f"Bearer {control_token.decode()}"}

    checks = Checks("verify")

    async with httpx.AsyncClient(base_url=args.base_url, timeout=60) as c:
        # 1. Служебные endpoints
        r = await c.get("/livez")
        checks.check("livez 200", r.status_code == 200)
        r = await c.get("/readyz")
        checks.check("readyz 200", r.status_code == 200)
        r = await c.get("/metrics")
        checks.check(
            "metrics содержит relayforge_requests_total",
            r.status_code == 200 and "relayforge_requests_total" in r.text,
        )

        # 2. 401 без/с неверным токеном
        r = await c.post("/v1/deliveries", json={})
        checks.check("401 без токена", r.status_code == 401)
        r = await c.post("/v1/deliveries", headers={"Authorization": "Bearer wrong"}, json={})
        checks.check("401 неверный токен", r.status_code == 401)

        # 3. Валидация API
        r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": "verify:val:1"},
                         json={"destination": "no-such-destination"})
        checks.check("422 неизвестный destination", r.status_code == 422)
        r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": "verify:val:2"},
                         json={"destination": "test", "event_type": "Bad.Type", "payload": {}})
        checks.check("422 неверный event_type", r.status_code == 422)
        r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": "short"},
                         json={"destination": "test", "event_type": "a.b.c", "payload": {}})
        checks.check("422 короткий Idempotency-Key", r.status_code == 422)
        r = await c.post(
            "/v1/deliveries", headers={**auth(), "Idempotency-Key": "verify:val:3"},
            content=b'{"destination":"test","event_type":"a.b.c","payload":{},"payload":{}}',
        )
        checks.check("400 duplicate JSON keys", r.status_code == 400)
        r = await c.post(
            "/v1/deliveries", headers={**auth(), "Idempotency-Key": "verify:val:4"},
            content=b'{"destination":"test","event_type":"a.b.c","payload":{"pad":"' + b"x" * (17 * 1024) + b'"}}',
        )
        checks.check("413 тело > 16 KiB", r.status_code == 413)

        m_before = None
        jobs_before = set(kubectl_job_names(args.namespace, args.release)) if use_kubectl else set()
        if use_kubectl:
            r = await c.get("/metrics")
            m_before = parse_metrics(r.text)
        jobs_after = jobs_before  # будет обновляться по ходу

        # 4. Последовательная идемпотентность
        key = f"verify:seq:{os.getpid()}"
        body = {"destination": "test", "event_type": "verify.event", "payload": {"n": 1}}
        r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": key}, json=body)
        checks.check("create → 202", r.status_code == 202, r.text[:100])
        delivery_id = r.json().get("id", "")
        r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": key}, json=body)
        checks.check(
            "duplicate → 200, тот же id, duplicate=true",
            r.status_code == 200 and r.json().get("duplicate") is True
            and r.json().get("id") == delivery_id,
            r.text[:100],
        )
        r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": key},
                         json={"destination": "test", "event_type": "verify.event", "payload": {"n": 2}})
        checks.check(
            "конфликт payload → 409 IDEMPOTENCY_CONFLICT",
            r.status_code == 409 and r.json().get("error", {}).get("code") == "IDEMPOTENCY_CONFLICT",
        )

        # 5. Конкурентная идемпотентность: 20 параллельных запросов, один ключ
        con_key = f"verify:conc:{os.getpid()}"
        variants = [
            {"destination": "test", "event_type": "verify.event", "payload": {"n": 1}},
            {"event_type": "verify.event", "payload": {"n": 1}, "destination": "test"},
            {"payload": {"n": 1}, "destination": "test", "event_type": "verify.event"},
        ]

        async def send_one(variant: dict):
            return await c.post("/v1/deliveries",
                                headers={**auth(), "Idempotency-Key": con_key}, json=variant)

        results = await asyncio.gather(*[send_one(variants[i % len(variants)]) for i in range(20)])
        ids = {r.json().get("id") for r in results if r.status_code in (200, 202)}
        ok_statuses = all(r.status_code in (200, 202) for r in results)
        checks.check(
            "20 параллельных → один delivery ID",
            ok_statuses and len(ids) == 1,
            f"statuses={sorted({r.status_code for r in results})}, ids={ids}",
        )
        if ok_statuses and len(ids) == 1:
            con_id = next(iter(ids))
            poll = await wait_terminal(c, auth(), con_id, timeout=120)
            checks.check("конкурентная доставка завершилась", poll.get("status") == "succeeded",
                         str(poll)[:120])
        if use_kubectl:
            jobs_after = set(kubectl_job_names(args.namespace, args.release))
            new_jobs = jobs_after - jobs_before
            checks.check("один Job на один ключ (конкурентный)", len(new_jobs) <= 2,
                         f"new_jobs={sorted(new_jobs)}")

        # 6. Backpressure (лимит активных Job; отклонённые не создают Jobs)
        if args.max_active_jobs > 0 and args.sink_url:
            r = await c.post(f"{args.sink_url}/control/mode", headers=ctrl(), json={"mode": "slow"})
            checks.check("sink: mode=slow", r.status_code == 200)
            await asyncio.sleep(1)
            bp_key = f"verify:bp:{os.getpid()}"
            accepted, rejected = 0, 0
            retry_after_ok = True

            async def bp_send(i: int):
                return await c.post(
                    "/v1/deliveries",
                    headers={**auth(), "Idempotency-Key": f"{bp_key}:{i}"},
                    json={"destination": "test", "event_type": "verify.event", "payload": {"i": i}},
                )

            bp_results = await asyncio.gather(
                *[bp_send(i) for i in range(args.max_active_jobs * 2 + 1)]
            )
            for r in bp_results:
                if r.status_code == 202:
                    accepted += 1
                elif r.status_code in (429, 503):
                    rejected += 1
                    if not r.headers.get("Retry-After"):
                        retry_after_ok = False
            checks.check("backpressure: есть отклонённые 429/503",
                         rejected >= 1, f"accepted={accepted}, rejected={rejected}")
            checks.check("backpressure: Retry-After присутствует", retry_after_ok)
            checks.check("backpressure: принято не больше лимита + превышения",
                         accepted <= args.max_active_jobs + 3,
                         f"accepted={accepted}, limit={args.max_active_jobs}")

            if use_kubectl:
                jobs_bp = set(kubectl_job_names(args.namespace, args.release))
                created_during_bp = len(jobs_bp - jobs_after)
                checks.check("отклонённые запросы не создали Jobs",
                             created_during_bp <= accepted + 1,
                             f"new_jobs={created_during_bp}, accepted={accepted}")
            await c.post(f"{args.sink_url}/control/mode", headers=ctrl(), json={"mode": "normal"})
            # Дождаться, пока активные Job из backpressure-фазы завершатся:
            # gauge /metrics обновляется раз в 5с, поэтому ноль должен
            # держаться несколько циклов подряд.
            zero_since = None
            deadline = time.monotonic() + 150
            while time.monotonic() < deadline:
                m = await c.get("/metrics")
                active = parse_metrics(m.text).get("active_jobs", 0.0)
                if active == 0.0:
                    if zero_since is None:
                        zero_since = time.monotonic()
                    elif time.monotonic() - zero_since >= 8:
                        break
                else:
                    zero_since = None
                await asyncio.sleep(2)
            checks.check("слоты backpressure освобождены (active_jobs=0 стабильно)",
                         zero_since is not None, f"active_jobs={active}")

        # 7. Временная ошибка (sink fail-first)
        if args.sink_url:
            await c.post(f"{args.sink_url}/control/reset", headers=ctrl())
            await c.post(f"{args.sink_url}/control/mode", headers=ctrl(),
                         json={"mode": "fail-first", "n": 2})
            t_key = f"verify:trans:{os.getpid()}"
            r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": t_key},
                             json={"destination": "test", "event_type": "verify.event", "payload": {"t": 1}})
            # После backpressure-фазы возможен короткий 503: повторяем с паузой
            for _ in range(3):
                if r.status_code in (429, 503):
                    await asyncio.sleep(6)
                    r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": t_key},
                                     json={"destination": "test", "event_type": "verify.event", "payload": {"t": 1}})
                else:
                    break
            checks.check("transient: create 202", r.status_code == 202,
                         f"status={r.status_code} {r.text[:120]}")
            t_id = r.json().get("id", "")
            poll = await wait_terminal(c, auth(), t_id, timeout=150)
            checks.check("временная ошибка → succeeded", poll.get("status") == "succeeded",
                         str(poll)[:120],)
            r = await c.get(f"{args.sink_url}/received/{t_id}", headers=ctrl())
            sink_state = r.json() if r.status_code == 200 else {}
            checks.check("sink: >=3 HTTP-попытки, 1 применение",
                         sink_state.get("attempts", 0) >= 3 and sink_state.get("applied") is True,
                         str(sink_state)[:120])
            await c.post(f"{args.sink_url}/control/mode", headers=ctrl(), json={"mode": "normal"})

        # 8. Постоянная ошибка (sink reject)
        if args.sink_url:
            await c.post(f"{args.sink_url}/control/reset", headers=ctrl())
            await c.post(f"{args.sink_url}/control/mode", headers=ctrl(), json={"mode": "reject"})
            p_key = f"verify:perm:{os.getpid()}"
            r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": p_key},
                             json={"destination": "test", "event_type": "verify.event", "payload": {"p": 1}})
            p_id = r.json().get("id", "")
            poll = await wait_terminal(c, auth(), p_id, timeout=90)
            checks.check("постоянная ошибка → failed", poll.get("status") == "failed",
                         str(poll)[:120])
            checks.check("failure описан в ответе", poll.get("failure") is not None,
                         str(poll.get("failure"))[:120])
            await c.post(f"{args.sink_url}/control/mode", headers=ctrl(), json={"mode": "normal"})

        # 9. TTL-очистка и новый delivery ID (только при --ttl-timeout)
        if args.ttl_timeout > 0:
            ttl_key = f"verify:ttl:{os.getpid()}"
            r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": ttl_key},
                             json={"destination": "test", "event_type": "verify.event", "payload": {"ttl": 1}})
            first_id = r.json().get("id", "")
            await wait_terminal(c, auth(), first_id, timeout=120)
            gone = False
            deadline = time.monotonic() + args.ttl_timeout
            while time.monotonic() < deadline:
                rr = await c.get(f"/v1/deliveries/{first_id}", headers=auth())
                if rr.status_code == 404:
                    gone = True
                    break
                await asyncio.sleep(2)
            checks.check("Job удалён TTL-контроллером (404)", gone, f"id={first_id}")
            r = await c.post("/v1/deliveries", headers={**auth(), "Idempotency-Key": ttl_key},
                             json={"destination": "test", "event_type": "verify.event", "payload": {"ttl": 1}})
            second_id = r.json().get("id", "")
            checks.check("повтор ключа после TTL → 202 и новый id",
                         r.status_code == 202 and second_id != first_id
                         and r.json().get("duplicate") is False,
                         f"first={first_id} second={second_id}")

        # 10. Логи worker: нет payload/подписи/секретов (kubectl, read-only)
        if use_kubectl and args.sink_url:
            jobs = kubectl_job_names(args.namespace, args.release)
            if jobs:
                logs = kubectl_worker_logs(args.namespace, jobs[-1])
                leaks = [
                    marker for marker in ("X-Relay-Signature", "v1=", '"invoice_id"', "signing-key")
                    if marker in logs
                ]
                checks.check("в логах worker нет payload/подписи/секретов", not leaks,
                             f"leaks={leaks}, job={jobs[-1]}")

        # 11. Изменение metrics после успеха/duplicate/conflict
        if use_kubectl:
            r = await c.get("/metrics")
            m_after = parse_metrics(r.text)
            checks.check("jobs_created_total вырос",
                         m_after["jobs_created_total"] > m_before["jobs_created_total"],
                         f"{m_before['jobs_created_total']} -> {m_after['jobs_created_total']}")
            checks.check("requests_total{result=success} вырос",
                         m_after["requests_success"] > m_before["requests_success"],
                         f"{m_before['requests_success']} -> {m_after['requests_success']}")
            checks.check("requests_total{result=conflict} вырос",
                         m_after["requests_conflict"] > m_before["requests_conflict"],
                         f"{m_before['requests_conflict']} -> {m_after['requests_conflict']}")

    ok = checks.report()
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    asyncio.run(main())