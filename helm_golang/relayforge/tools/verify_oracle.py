#!/usr/bin/env python3
"""Pin orjson float-notation threshold, overflow and surrogate behavior so the
Go port can match them exactly."""
import json
import orjson
import sys

print("orjson", orjson.__version__)
print("--- scientific vs fixed threshold ---")
for v in ["1e-4", "5e-5", "1e-5", "2e-5", "9.999e-5", "1e-6", "1e-7", "1.5e-5",
          "1e14", "1e15", "1e16", "1e17", "1000000000000000.0", "999999999999999.0"]:
    f = json.loads(v)
    print(f"{v:24} -> {orjson.dumps({'p': f}).decode()}")

print("--- overflow 1e309 ---")
try:
    print(orjson.dumps({"p": json.loads("1e309")}).decode())
except Exception as e:
    print("orjson err", type(e).__name__, e)

print("--- lone surrogates ---")
for s in [r'"\ud800"', r'"\udc00"', r'"\ud800\udc00"', r'"a\ud800b"']:
    try:
        v = json.loads(s)
        print(s, "->", orjson.dumps({"p": v}).decode())
    except Exception as e:
        print(s, "-> err", type(e).__name__, e)

print("--- int overflow (>64-bit) through pipeline ---")
try:
    print(orjson.dumps({"p": json.loads("123456789012345678901234567890")}).decode())
except Exception as e:
    print("int overflow err", type(e).__name__, e)
try:
    print(orjson.dumps({"p": 2 ** 64}).decode())
except Exception as e:
    print("2^64 err", type(e).__name__, e)