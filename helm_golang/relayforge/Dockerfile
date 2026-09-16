# Единый образ для всех 5 режимов (api, worker, test-sink, helm-test, cleanup).
# Внутри — только статический Go-бинарник: НЕТ shell, НЕТ Python, НЕТ kubectl.
# Какой режим запускать, решает chart через exec-form command: ["/relayforge", "<mode>"].

# ---------------------------------------------------------------------------
# Stage 1: builder — сборка статического бинарника из ./cmd/relayforge
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Сначала только манифесты зависимостей: слой кэшируется и не инвалидируется
# при каждом изменении исходников. Шаблон go.sum* не ломает сборку, если
# зависимостей нет и go.sum отсутствует.
COPY go.mod go.sum* ./
RUN go mod download

# Затем весь остальной Go-исходник (cmd/, internal/, ... — см. .dockerignore).
COPY . .

# CGO_ENABLED=0  — полностью статический бинарник (работает на distroless/static).
# -trimpath      — без абсолютных путей сборки в бинарнике.
# -ldflags="-s -w" — убираем symbol table и DWARF (меньше размер).
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /relayforge ./cmd/relayforge

# ---------------------------------------------------------------------------
# Stage 2: runtime — distroless static (без shell, без пакетного менеджера)
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# ENTRYPOINT намеренно НЕ задаём: chart запускает контейнер exec-form
# command: ["/relayforge", "<mode>"], mode ∈ api|worker|test-sink|helm-test|cleanup.
# K8s command переопределяет и ENTRYPOINT, и CMD образа, поэтому CMD ниже —
# только удобный дефолт для `docker run` без аргументов.
COPY --from=builder /relayforge /relayforge

# uid/gid 1000:1000 совпадает с pod/container securityContext runAsUser: 1000
# в chart. Базовый образ по умолчанию — nonroot (65532); явное выравнивание
# убирает расхождение между USER образа и runAsUser, из-за которого локальный
# `docker run` (без k8s) и кластер запускали бы процесс под разными uid.
USER 1000:1000

CMD ["api"]
