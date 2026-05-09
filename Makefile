CONTROLLER_GEN_VERSION ?= v0.14.0
ENVTEST_VERSION        ?= release-0.17
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

.PHONY: all build test lint generate manifests docker-build install deploy undeploy

all: build

build:
	go build -o bin/sandbox-warm-pool-controller ./cmd/

test:
	KUBEBUILDER_ASSETS="$(shell go run sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION) use --print path)" \
	go test ./... -v -count=1 -timeout 120s

lint:
	golangci-lint run ./...

generate:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

manifests:
	$(CONTROLLER_GEN) rbac:roleName=sandbox-warm-pool-controller crd \
	  paths="./api/..." paths="./internal/controller/..." \
	  output:crd:artifacts:config=config/crd \
	  output:rbac:artifacts:config=config/rbac

docker-build:
	docker build -t ghcr.io/g26karthik/sandbox-warm-pool-controller:latest .

install:
	kubectl apply -f config/crd/

deploy:
	kubectl apply -f config/rbac/
	kubectl apply -f config/manager/

undeploy:
	kubectl delete -f config/manager/ --ignore-not-found
	kubectl delete -f config/rbac/ --ignore-not-found
	kubectl delete -f config/crd/ --ignore-not-found
