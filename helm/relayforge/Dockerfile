FROM python:3.12.7-slim-bookworm

WORKDIR /app

COPY pyproject.toml uv.lock ./
RUN pip install --no-cache-dir uv && uv sync --frozen

COPY src/ ./src/

ENTRYPOINT ["python", "-m", "relayforge"]
CMD ["api"]
