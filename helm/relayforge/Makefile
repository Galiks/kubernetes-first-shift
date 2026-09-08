.PHONY: cluster build publish install test uninstall

cluster:
	k3d cluster create relayforge --servers 1 --agents 2 -p "8080:8080@loadbalancer" --registry-create k3d-relayforge-registry:5050 --wait || true

build:
	docker build -t localhost:5050/relayforge:0.1.0 .
	docker push localhost:5050/relayforge:0.1.0

publish:
	helm package chart/relayforge --destination .helm-charts
	helm push .helm-charts/relayforge-0.1.0.tgz oci://localhost:5050/charts

install:
	helm upgrade --install relay-a oci://localhost:5050/charts/relayforge --version 0.1.0 -f chart/relayforge/values-relay-a.yaml --set image.digest=sha256:placeholder --wait --timeout 5m --rollback-on-failure --server-side=true

test:
	helm test relay-a --logs -n relayforge

uninstall:
	helm uninstall relay-a -n relayforge


# Evidence collection
.PHONY: evidence evidence-redact evidence-summary

evidence-redact:
	./evidence/scripts/redact.sh evidence/

evidence-summary:
	./evidence/scripts/collect-summary.sh

evidence: evidence-redact evidence-summary
	@echo "✓ Evidence prepared in evidence/summary.txt"
