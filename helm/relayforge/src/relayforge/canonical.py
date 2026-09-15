import json

import orjson


def _reject_special(value):
    raise ValueError(f"special float not allowed: {value}")


def _object_pairs_hook(pairs):
    """Строит dict из пар парсера JSON, отклоняя повторяющиеся ключи."""
    data = {}
    for key, value in pairs:
        if key in data:
            raise ValueError(f"duplicate key: {key}")
        data[key] = value
    return data


def strict_json_loads(text: str) -> dict:
    """Строгий разбор JSON: duplicate keys, NaN, Infinity — ошибка."""
    try:
        data = json.loads(
            text,
            object_pairs_hook=_object_pairs_hook,
            parse_constant=_reject_special,
        )
    except (json.JSONDecodeError, UnicodeDecodeError, ValueError) as e:
        raise ValueError(f"invalid JSON: {e}") from e
    if not isinstance(data, dict):
        raise ValueError("root must be an object")
    return data


def canonicalize(data: dict) -> bytes:
    # orjson сохраняет различие int/float: 1 != 1.0; ключи сортируются.
    return orjson.dumps(data, option=orjson.OPT_SORT_KEYS)