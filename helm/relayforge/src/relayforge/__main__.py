import sys
from relayforge import api, worker, test_sink, helm_test, cleanup

MODES = {
    "api": api.run,
    "worker": worker.run,
    "test-sink": test_sink.run,
    "helm-test": helm_test.run,
    "cleanup": cleanup.run,
}

def main() -> None:
    if len(sys.argv) < 2 or sys.argv[1] not in MODES:
        print(
            f"Usage: python -m relayforge <{'|'.join(MODES)}>",
            file=sys.stderr,
        )
        sys.exit(2)
    MODES[sys.argv[1]]()

if __name__ == "__main__":
    main()