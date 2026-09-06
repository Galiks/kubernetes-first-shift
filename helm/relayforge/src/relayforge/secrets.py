from pathlib import Path

CLIENT_TOKEN_PATH = Path("/etc/relayforge/secrets/client-token")
SIGNING_KEY_PATH = Path("/etc/relayforge/secrets/signing-key")
VERIFICATION_KEY_PATH = Path("/etc/relayforge/secrets/verification-key")
CONTROL_TOKEN_PATH = Path("/etc/relayforge/secrets/control-token")


def read_secret(path: Path) -> bytes:
    with path.open("rb") as f:
        return f.read().rstrip(b"\n")