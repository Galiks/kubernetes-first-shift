import sys
import asyncio

async def run_api(): print("API mode stub")
async def run_worker(): print("Worker mode stub")
async def run_test_sink(): print("Test-sink mode stub")
async def run_helm_test(): print("Helm-test mode stub")
async def run_cleanup(): print("Cleanup mode stub")

MODES = {
    "api": run_api,
    "worker": run_worker,
    "test-sink": run_test_sink,
    "helm-test": run_helm_test,
    "cleanup": run_cleanup,
}

def main():
    if len(sys.argv) < 2 or sys.argv[1] not in MODES:
        print(f"Usage: python -m relayforge <{'|'.join(MODES)}>", file=sys.stderr)
        sys.exit(2)
    
    mode = sys.argv[1]
    print(f"Starting RelayForge in '{mode}' mode...")
    asyncio.run(MODES[mode]())

if __name__ == "__main__":
    main()
