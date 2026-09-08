import json
import datetime
from pathlib import Path

RECEIPTS_DIR = Path("/var/lib/relayforge-test-sink/receipts")


class ReceiptStore:
    def __init__(self):
        RECEIPTS_DIR.mkdir(parents=True, exist_ok=True)

    def save(self, delivery_id: str) -> None:
        path = RECEIPTS_DIR / f"{delivery_id}.json"
        path.write_text(json.dumps({
            "delivery_id": delivery_id,
            "applied_at": datetime.datetime.now(datetime.UTC).isoformat(),
        }))

    def exists(self, delivery_id: str) -> bool:
        return (RECEIPTS_DIR / f"{delivery_id}.json").exists()

    def clear(self) -> None:
        for f in RECEIPTS_DIR.glob("*.json"):
            f.unlink()