import json
import orjson


class _DuplicateKeyChecker(dict):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._seen: set = set()

    def __setitem__(self, key, value):
        if key in self._seen:
            raise ValueError(f"duplicate key: {key}")
        self._seen.add(key)
        super().__setitem__(key, value)


def _reject_special(value):
    raise ValueError(f"special float not allowed: {value}")


def strict_json_loads(text: str) -> dict:
    try:
        data = json.loads(
            text,
            object_pairs_hook=_DuplicateKeyChecker,
            parse_constant=_reject_special,
        )
    except (json.JSONDecodeError, UnicodeDecodeError, ValueError) as e:
        raise ValueError(f"invalid JSON: {e}") from e
    if not isinstance(data, dict):
        raise ValueError("root must be an object")
    return data


def canonicalize(data: dict) -> bytes:
    # orjson сохраняет различие int/float: 1 != 1.0
    return orjson.dumps(data, option=orjson.OPT_SORT_KEYS)