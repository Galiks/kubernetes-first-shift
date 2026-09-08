import hashlib
import hmac

def sign(timestamp: str, delivery_id: str, body: bytes, key: bytes) -> str:
    message = f"{timestamp}\n{delivery_id}\n".encode("utf-8") + body
    return hmac.new(key, message, hashlib.sha256).hexdigest()

def verify(timestamp: str, delivery_id: str, body: bytes, signature: str, key: bytes) -> bool:
    expected = sign(timestamp, delivery_id, body, key)
    return hmac.compare_digest(f"v1={expected}", signature)