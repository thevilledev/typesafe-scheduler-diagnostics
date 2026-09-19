IMAGE ?= typesafe-scheduler-diagnostics:dev
KIND_CLUSTER ?= typesafe-diagnostics
KIND_NODE_IMAGE ?= kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed
TYPESAFE_ENV ?= typesafe-env

.PHONY: build demo demo-clean deploy image kind-demo kind-down kind-load kind-up secret test verify-demo

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -o bin/typesafe-scheduler ./cmd/typesafe-scheduler

image:
	docker build --tag $(IMAGE) .

kind-up:
	@if kind get clusters | grep -qx $(KIND_CLUSTER); then \
		echo "kind cluster $(KIND_CLUSTER) already exists"; \
	else \
		kind create cluster --name $(KIND_CLUSTER) --image $(KIND_NODE_IMAGE) --config config/kind.yaml; \
	fi

kind-load:
	kind load docker-image $(IMAGE) --name $(KIND_CLUSTER)

secret:
	@test -f $(TYPESAFE_ENV) || { echo "missing TypeSafe environment file: $(TYPESAFE_ENV)" >&2; exit 1; }
	kubectl apply -f config/deploy/namespace.yaml
	kubectl --namespace typesafe-system create secret generic typesafe-api \
		--from-env-file=$(TYPESAFE_ENV) --dry-run=client -o yaml | kubectl apply -f -

deploy:
	kubectl apply -f config/deploy
	kubectl --namespace typesafe-system rollout status deployment/typesafe-scheduler --timeout=120s

demo:
	kubectl apply -f config/demo.yaml

verify-demo:
	hack/verify-demo.sh

demo-clean:
	kubectl delete -f config/demo.yaml --ignore-not-found

kind-down:
	kind delete cluster --name $(KIND_CLUSTER)

kind-demo:
	$(MAKE) kind-up
	$(MAKE) image
	$(MAKE) kind-load
	$(MAKE) secret TYPESAFE_ENV=$(TYPESAFE_ENV)
	$(MAKE) deploy
	$(MAKE) demo
	$(MAKE) verify-demo
