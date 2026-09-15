FROM python:3.12.7-slim-bookworm

WORKDIR /app

COPY pyproject.toml uv.lock ./
# src копируется до uv sync: пакет ставится editable из src-раскладки.
COPY src/ ./src/

RUN pip install --no-cache-dir uv && uv sync --frozen

# uv sync ставит зависимости и пакет в /app/.venv. Делаем его python
# доступным как `python`: chart запускает режимы через `command: ["python", ...]`
# (exec form), который переопределяет ENTRYPOINT образа.
ENV PATH="/app/.venv/bin:$PATH"

ENTRYPOINT ["/app/.venv/bin/python", "-m", "relayforge"]
CMD ["api"]