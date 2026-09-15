import json
import os
from pathlib import Path

RELEASE = os.environ["RELAYFORGE_RELEASE"]
NAMESPACE = os.environ["RELAYFORGE_NAMESPACE"]
API_PORT = int(os.environ.get("RELAYFORGE_API_PORT", "8080"))
TEST_SINK_PORT = int(os.environ.get("RELAYFORGE_TEST_SINK_PORT", "8080"))
# Внутрикластерный URL test-sink (для UI: управление режимами и проверки).
SINK_URL = os.environ.get(
    "RELAYFORGE_SINK_URL", f"http://{RELEASE}-relayforge-test-sink:8080"
)
# Control-token монтируется в API-pod для управления test-sink из UI.
CONTROL_TOKEN_SINK_PATH = Path("/etc/relayforge/secrets-control/control-token")

DESTINATIONS_PATH = Path("/etc/relayforge/config/destinations.json")

# Параметры worker-подa (передаются в Pod template создаваемых Jobs).
IMAGE_REPOSITORY = os.environ.get("RELAYFORGE_IMAGE_REPOSITORY", "localhost:5050/relayforge")
IMAGE_DIGEST = os.environ.get("RELAYFORGE_IMAGE_DIGEST", "")
SIGNING_SECRET_NAME = os.environ.get("RELAYFORGE_SIGNING_SECRET_NAME", "")
SIGNING_SECRET_KEY = os.environ.get("RELAYFORGE_SIGNING_SECRET_KEY", "signing-key")
DESTINATIONS_CONFIGMAP = os.environ.get(
    "RELAYFORGE_DESTINATIONS_CONFIGMAP", f"{RELEASE}-relayforge-destinations"
)
# ServiceAccount worker-пода (полное имя из chart: {fullname}-worker).
WORKER_SERVICE_ACCOUNT = os.environ.get(
    "RELAYFORGE_WORKER_SERVICE_ACCOUNT", f"{RELEASE}-relayforge-worker"
)

# Параметры delivery-Job.
WORKER_BACKOFF_LIMIT = int(os.environ.get("RELAYFORGE_WORKER_BACKOFF_LIMIT", "3"))
WORKER_ACTIVE_DEADLINE = int(os.environ.get("RELAYFORGE_WORKER_ACTIVE_DEADLINE", "300"))
WORKER_TTL = int(os.environ.get("RELAYFORGE_WORKER_TTL", "600"))
WORKER_PERMANENT_EXIT_CODE = int(os.environ.get("RELAYFORGE_WORKER_PERMANENT_EXIT_CODE", "12"))
WORKER_CONNECT_TIMEOUT = float(os.environ.get("RELAYFORGE_WORKER_CONNECT_TIMEOUT", "5"))
WORKER_READ_TIMEOUT = float(os.environ.get("RELAYFORGE_WORKER_READ_TIMEOUT", "10"))

# Ротация секретов: несекретное значение; новая revision попадает в следующие Jobs.
SECRET_REVISION = os.environ.get("RELAYFORGE_SECRET_REVISION", "1")

# Backpressure API.
API_BACKPRESSURE_MAX_ACTIVE = int(os.environ.get("RELAYFORGE_API_BACKPRESSURE_MAX_ACTIVE", "10"))
API_BACKPRESSURE_MAX_CONCURRENT = int(os.environ.get("RELAYFORGE_API_BACKPRESSURE_MAX_CONCURRENT", "2"))

# Fault injection (только test profile, RELAYFORGE_TASK.md, сценарий 02):
# один раз сделать вид, что create Job не вернул результат (5xx),
# хотя API server Job принял. Не влияет на аутентификацию и обычный путь.
TEST_FAULT_CREATE_TIMEOUT = (
    os.environ.get("RELAYFORGE_TEST_FAULT_CREATE_TIMEOUT", "0").lower() in ("1", "true", "yes")
)


def load_destinations() -> dict:
    """Читает destinations из ConfigMap (смонтирован read-only файлом)."""
    with DESTINATIONS_PATH.open("r", encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, dict):
        raise ValueError("destinations must be an object")
    return data


def get_destination_url(name: str, destinations: dict) -> str:
    """Возвращает URL внешнего приёмника по имени destination из конфигурации."""
    if name not in destinations:
        raise KeyError(f"unknown destination: {name}")
    url = destinations[name].get("url")
    if not isinstance(url, str) or not url:
        raise ValueError(f"destination {name} has no url")
    return url