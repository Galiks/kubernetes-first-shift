import datetime
import logging
import sys

import orjson


class JsonFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        data = {
            "ts": datetime.datetime.now(datetime.UTC).isoformat(),
            "level": record.levelname,
            "logger": record.name,
            "msg": record.getMessage(),
        }
        extra = getattr(record, "extra_data", None)
        if extra and isinstance(extra, dict):
            # Запрещено: payload, signature, secrets, tokens
            forbidden = {"payload", "signature", "secret", "token", "password"}
            for k in forbidden:
                extra.pop(k, None)
            data.update(extra)
        return orjson.dumps(data).decode("utf-8")


def setup_logging() -> None:
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(JsonFormatter())
    root = logging.getLogger()
    root.handlers.clear()
    root.addHandler(handler)
    root.setLevel(logging.INFO)