def classify(status: int) -> str:
    if 200 <= status < 300:
        return "success"
    if status in (408, 429) or 500 <= status < 600:
        return "transient"
    if 400 <= status < 500:
        return "permanent"
    if 300 <= status < 400:
        return "permanent"  # redirect — ошибка
    return "transient"