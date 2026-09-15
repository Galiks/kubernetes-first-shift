import datetime
import json
from pathlib import Path

RECEIPTS_DIR = Path("/var/lib/relayforge-test-sink/receipts")


class ReceiptStore:
    """Тестовые receipts на emptyDir: применение и счётчик HTTP-попыток.

    Файлы переживают restart контейнера (тот же Pod), но не замену Pod —
    это и проверяется в сценарии 10. Попытки хранятся отдельно от факта
    применения события.
    """

    def __init__(self, directory: Path | None = None):
        self._dir = directory or RECEIPTS_DIR
        self._dir.mkdir(parents=True, exist_ok=True)

    def _path(self, delivery_id: str) -> Path:
        return self._dir / f"{delivery_id}.json"

    def _read(self, delivery_id: str) -> dict | None:
        try:
            with self._path(delivery_id).open("r", encoding="utf-8") as f:
                data = json.load(f)
            return data if isinstance(data, dict) else None
        except (OSError, json.JSONDecodeError):
            return None

    def _write(self, delivery_id: str, data: dict) -> None:
        path = self._path(delivery_id)
        tmp = path.with_suffix(".tmp")
        tmp.write_text(json.dumps(data, ensure_ascii=False))
        tmp.replace(path)

    def record_attempt(self, delivery_id: str) -> int:
        """Увеличивает счётчик HTTP-попыток; возвращает новое значение."""
        data = self._read(delivery_id) or {"delivery_id": delivery_id, "applied": False}
        data["attempts"] = int(data.get("attempts", 0)) + 1
        self._write(delivery_id, data)
        return data["attempts"]

    def apply(self, delivery_id: str) -> None:
        """Отмечает событие применённым (один раз)."""
        data = self._read(delivery_id) or {
            "delivery_id": delivery_id,
            "applied": False,
            "attempts": 0,
        }
        data["applied"] = True
        data["applied_at"] = datetime.datetime.now(datetime.UTC).isoformat()
        self._write(delivery_id, data)

    def is_applied(self, delivery_id: str) -> bool:
        data = self._read(delivery_id)
        return bool(data and data.get("applied"))

    def attempts(self, delivery_id: str) -> int:
        data = self._read(delivery_id)
        return int(data.get("attempts", 0)) if data else 0

    def clear(self) -> None:
        for f in self._dir.glob("*.json"):
            f.unlink()

    def count(self) -> int:
        return len(list(self._dir.glob("*.json")))