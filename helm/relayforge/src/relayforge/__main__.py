import asyncio
import importlib
import sys

# Один application image, пять режимов. Функции могут быть sync или async.
MODES = {
    "api": ("relayforge.api.app", "main"),
    "worker": ("relayforge.worker.run", "run"),
    "test-sink": ("relayforge.test_sink.app", "main"),
    "helm-test": ("relayforge.helm_test.run", "run"),
    "cleanup": ("relayforge.cleanup.run", "run"),
}


def main() -> None:
    if len(sys.argv) < 2 or sys.argv[1] not in MODES:
        print(f"Usage: python -m relayforge <{'|'.join(MODES)}>", file=sys.stderr)
        sys.exit(2)

    mode = sys.argv[1]
    module_name, func_name = MODES[mode]
    module = importlib.import_module(module_name)
    fn = getattr(module, func_name)

    result = fn()
    if asyncio.iscoroutine(result):
        asyncio.run(result)


if __name__ == "__main__":
    main()