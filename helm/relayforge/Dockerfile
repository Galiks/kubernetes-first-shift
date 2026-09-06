FROM python:3.12.7-slim-bookworm@sha256:<digest> AS base

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1

RUN groupadd -r relayforge && useradd -r -g relayforge -u 1000 -d /app -s /sbin/nologin relayforge \
 && mkdir -p /app /var/lib/relayforge-test-sink/receipts \
 && chown -R relayforge:relayforge /app /var/lib/relayforge-test-sink

WORKDIR /app

COPY pyproject.toml uv.lock ./
RUN pip install --no-cache-dir uv \
 && uv sync --frozen --no-dev --no-install-project

COPY src/ ./src/
RUN uv sync --frozen --no-dev

USER 1000:1000

ENTRYPOINT ["python", "-m", "relayforge"]
CMD ["api"]