#!/usr/bin/env python3
"""Сборка дифференциального корпуса канонизации для Go-порта.

Оракул — фактический Python-модуль (helm/relayforge/src/relayforge/canonical.py).
Конвейер совпадает с runtime: strict_json_loads (строгий парсинг: дубликаты ключей,
NaN/Infinity, не-объект root) -> canonicalize (orjson OPT_SORT_KEYS).
Пишет testdata/canonical.jsonl:
  {"input": <raw>, "expected_error": bool, "canonical": <str|null>, "sha256": <hex|null>}
Go golden-тест должен совпасть 1:1.
"""
import argparse
import hashlib
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "helm", "relayforge", "src"))
from relayforge.canonical import strict_json_loads, canonicalize  # noqa: E402

BASE = '{"destination":"d","event_type":"e","payload":%s}'


def samples():
    out = []
    for v in ["0", "1", "-1", "42", "123456789012345678901234567890",
              "-9223372036854775808", "9223372036854775807",
              "9223372036854775808", "18446744073709551615",
              "18446744073709551616", "-9223372036854775809", "-0",
              "1.0", "-1.0", "0.5", "3.14", "1e2", "1E+3", "1e-7", "1e21",
              "123.456e-9", "-0.0", "0.0", "5e-324", "1.7976931348623157e308",
              "1e-5", "5e-5", "9.999e-5", "1e-6", "1.5e-5", "1e14", "1e15", "1e16", "1e309",
              "1.2345678901234567", "12345678901234567890.123456789",
              "0.3333333333333333333333333333"]:
        out.append(BASE % v)
    out.append('{"destination":"d","event_type":"e","payload":{"a":{"b":[1,2,{"c":3}],"d":null},"list":[]}}')
    out.append(BASE % '{"z":1,"a":2,"m":{"y":1,"b":2}}')
    out.append(BASE % '{"ключ-я":1,"ключ-а":2,"ключ-б":3}')
    out.append(BASE % '{"é":1,"ä":2,"z":3,"a":4}')
    out.append(BASE % '"simple"')
    out.append(BASE % '"esc\\n\\t\\"\\u00e9"')
    out.append(BASE % '"международный"')
    out.append(BASE % '"<>&/\\u0001\\u001f"')
    out.append(BASE % '"\\u2028\\u2029"')
    out.append(BASE % '"\\b\\f\\u0000"')
    out.append(BASE % "true")
    out.append(BASE % "false")
    out.append(BASE % "null")
    out.append('{"destination":"d","event_type":"e","payload":{"emoji":"\\ud83d\\ude00","x":1}}')
    # Одиночные суррогаты: Python json принимает, но orjson.dumps падает (TypeError).
    out.append(r'{"destination":"d","event_type":"e","payload":"\ud800"}')
    out.append(r'{"destination":"d","event_type":"e","payload":"\udc00"}')
    out.append(r'{"destination":"d","event_type":"e","payload":"a\ud800b"}')
    # Экранированный обратный слэш: литерал "\ud800" — НЕ суррогат, валидно.
    out.append(r'{"destination":"d","event_type":"e","payload":"\\ud800"}')
    out.append('{"destination":"d","event_type":"e","payload":{}}')
    out.append('{"destination":"","event_type":"","payload":{}}')
    out.append(BASE % '{"a":1,"b":1.0}')
    # Ошибки парсинга.
    out.append('{"destination":"d","event_type":"e","payload":{"x":NaN}}')
    out.append('{"destination":"d","event_type":"e","payload":{"x":Infinity}}')
    out.append('{"destination":"d","event_type":"e","payload":{"a":1,"a":2}}')
    out.append(BASE % '{"a":1,"a":2}')
    out.append('{"destination":"d","event_type":"e","payload":{"a":-Infinity}}')
    out.append("[1,2]")
    out.append('"str"')
    out.append("null")
    out.append("true")
    out.append("42")
    out.append('{"destination":"d","event_type":"e","payload":}')  # malformed
    out.append('{"destination":"d","event_type":"e"}')  # missing payload -> STILL valid object
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=os.path.join(os.path.dirname(__file__), "..", "testdata", "canonical.jsonl"))
    args = ap.parse_args()
    rows, nerr = [], 0
    for raw in samples():
        try:
            data = strict_json_loads(raw)
            b = canonicalize(data)
            rows.append({"input": raw, "expected_error": False,
                         "canonical": b.decode("utf-8"),
                         "sha256": hashlib.sha256(b).hexdigest()})
        except Exception as e:  # strict parse / canonical error
            nerr += 1
            rows.append({"input": raw, "expected_error": True,
                         "canonical": None, "sha256": None, "why": type(e).__name__})
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as f:
        for r in rows:
            f.write(json.dumps(r, ensure_ascii=False, sort_keys=True) + "\n")
    print(f"wrote {len(rows)} rows ({len(rows)-nerr} valid, {nerr} expected-error) -> {args.out}")


if __name__ == "__main__":
    main()