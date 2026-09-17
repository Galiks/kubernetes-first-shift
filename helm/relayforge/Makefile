# Объявляем цели как "phony" (фиктивные), чтобы Make не искал файлы с такими именами
# и всегда выполнял команды, даже если файл с именем "build" или "test" существует.
.PHONY: cluster build publish install upgrade rollback test uninstall evidence evidence-redact evidence-summary

# -----------------------------------------------------------------------------
# 1. Инфраструктура и сборка
# -----------------------------------------------------------------------------

cluster:
	# Создаём локальный k3d-кластер с 1 server и 2 agent (минимум 2 schedulable node для HA).
	# -p "8080:8080@loadbalancer" пробрасывает порт для доступа к API извне.
	# --registry-create поднимает встроенный OCI registry на порту 5050 для push image и chart.
	# --wait дожидается готовности нод.
	# || true предотвращает ошибку, если кластер с таким именем уже существует.
	k3d cluster create relayforge --servers 1 --agents 2 -p "8080:8080@loadbalancer" --registry-create k3d-relayforge-registry:5050 --wait || true

build:
	# Собираем единый application image для всех 5 режимов (api, worker, test-sink, helm-test, cleanup).
	# Тег 0.1.0 используется для локальной сборки, но в production значениях будет зафиксирован SHA-256 digest.
	docker build -t localhost:5050/relayforge:0.1.0 .
	# Отправляем образ в локальный registry k3d, чтобы кластер мог его pull'ить.
	docker push localhost:5050/relayforge:0.1.0

publish:
	# Упаковываем Helm chart в архив .tgz в локальную директорию .helm-charts.
	helm package chart/relayforge --destination .helm-charts
	# Публикуем упакованный chart в локальный OCI registry.
	# --plain-http: локальный registry k3d работает по HTTP.
	helm push .helm-charts/relayforge-0.1.0.tgz oci://localhost:5050/charts --plain-http

# -----------------------------------------------------------------------------
# 2. Развёртывание и тестирование
# -----------------------------------------------------------------------------

install:
	# Единый гейтед-путь установки: scripts/install.sh (тот же путь, что и upgrade).
	# Скрипт требует cluster/image-digest.txt (создаёт scripts/build.sh), выполняет
	# helm lint --strict, local template и server dry-run, затем
	# helm upgrade --install с --wait=watcher, --timeout, --rollback-on-failure,
	# --server-side=true (Server-Side Apply / field ownership) и --plain-http.
	./scripts/install.sh relay-a relayforge

upgrade:
	# upgrade совпадает с install: оба используют scripts/install.sh с его гейтами
	# (digest guard, strict lint, local template, server dry-run) и helm upgrade --install.
	# Параметры — через аргументы либо переменные окружения скрипта (RELEASE, NAMESPACE).
	./scripts/install.sh

rollback:
	# Ручной rollback к предыдущей успешной revision: make rollback REV=<n>.
	@test -n "$(REV)" || (echo "usage: make rollback REV=<revision>"; exit 1)
	helm rollback relay-a $(REV) --namespace relayforge --wait --timeout 5m

test:
	# Запускаем Job 'helm-test', определённый в chart (hook: test).
	# Он отправляет тестовую доставку, проверяет идемпотентность, ждёт terminal status и сверяет результат с test-sink.
	# Флаг --logs выводит stdout/stderr Job'а для отладки в случае падения.
	# -n relayforge явно указывает namespace, так как chart не создаёт его сам.
	helm test relay-a --logs -n relayforge

uninstall:
	# Удаляем релиз. Это автоматически триггерит pre-delete hook (Job 'cleanup').
	# Cleanup сначала масштабирует API Deployment в 0, дожидается этого по status, 
	# а затем удаляет все delivery Jobs этого релиза, предотвращая orphaned ресурсы.
	helm uninstall relay-a -n relayforge

# -----------------------------------------------------------------------------
# 3. Сборка доказательств (Evidence)
# -----------------------------------------------------------------------------

# Объявляем цели evidence как phony.
.PHONY: evidence evidence-redact evidence-summary

evidence-redact:
	# Запускаем скрипт, который рекурсивно ищет в папке evidence/ чувствительные данные 
	# (Bearer токены, HMAC-подписи v1=..., полные payload) и заменяет их на <REDACTED>.
	# Это критичное требование задания: логи не должны содержать секретов.
	./evidence/scripts/redact.sh evidence/

evidence-summary:
	# Выполняем набор команд для финальной проверки (helm version, lint, template, dry-run, 
	# status, history, get values, auth can-i, endpointslice и т.д.) и сохраняем вывод в summary.txt.
	./evidence/scripts/collect-summary.sh

evidence: evidence-redact evidence-summary
	# Агрегирующая цель: сначала очищаем секреты, потом собираем итоговый отчёт.
	# Символ @ подавляет вывод самой команды echo в терминал.
	@echo "✓ Evidence prepared and redacted in evidence/summary.txt"