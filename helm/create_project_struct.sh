#!/usr/bin/env bash
mkdir -p relayforge/{src/relayforge/{api,worker,test_sink,helm_test,cleanup},tests,chart/relayforge/templates,cluster,scripts,evidence}
cd relayforge
touch pyproject.toml Dockerfile .dockerignore Makefile
touch README.md RUNBOOK.md SECURITY.md DECISIONS.md
touch src/relayforge/{__init__,__main__,config,logging,canonical,secrets}.py
touch src/relayforge/api/{__init__,app,routes,auth,validation,k8s_client,job_builder,backpressure,metrics,probes,errors}.py
touch src/relayforge/worker/{__init__,run,signer,http_client,classifier}.py
touch src/relayforge/test_sink/{__init__,app,routes,verifier,receipts,modes}.py
touch src/relayforge/helm_test/{__init__,run}.py
touch src/relayforge/cleanup/{__init__,run}.py