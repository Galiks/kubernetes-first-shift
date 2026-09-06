import json
import os
from pathlib import Path

RELEASE = os.environ["RELAYFORGE_RELEASE"]
NAMESPACE = os.environ["RELAYFORGE_NAMESPACE"]
API_PORT = int(os.environ.get("RELAYFORGE_API_PORT", "8080"))

DESTINATIONS_PATH = Path("/etc/relayforge/config/destinations.json")

def load_destinations() -> dict:
    with DESTINATIONS_PATH.open("r", encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, dict):
        raise ValueError("destinations must be an object")
    return data

def get_destination_url(name: str, destinations: dict) -> str:
    if name not in destinations:
        raise KeyError(f"unknown destination: {name}")
    url = destinations[name].get("url")
    if not isinstance(url, str) or not url:
        raise ValueError(f"destination {name} has no url")
    return url