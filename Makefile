# Every kubectl call pins the context so simulations can never land on another cluster.
KCTX    := docker-desktop
KUBECTL := kubectl --context $(KCTX)
COMPOSE := docker compose -f deployments/docker/compose.yml

.PHONY: build run test test-race lint migrate-up migrate-down db-up db-down \
	sim-up sim-down sim-status monitoring-up monitoring-down frontend-dev frontend-build frontend-lint

build:
	go build -o bin/ ./cmd/...

run:
	go run ./cmd/server

test:
	go test ./...

test-race:
	go test -race ./...

# gofmt -l lists unformatted files; empty output = clean.
lint:
	gofmt -l .
	go vet ./...

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

db-up:
	$(COMPOSE) up -d --wait

db-down:
	$(COMPOSE) down

sim-up:
	$(KUBECTL) apply -f deployments/kubernetes/sim/

sim-down:
	$(KUBECTL) delete namespace kubesentinel-sim --ignore-not-found

sim-status:
	$(KUBECTL) get pods -n kubesentinel-sim -o wide

# Prometheus in its own namespace, scraping kubelet cAdvisor; reachable at http://localhost:30090.
monitoring-up:
	$(KUBECTL) apply -f deployments/kubernetes/monitoring/

monitoring-down:
	$(KUBECTL) delete -f deployments/kubernetes/monitoring/ --ignore-not-found

frontend-dev:
	cd frontend && npm run dev

frontend-build:
	cd frontend && npm run build

frontend-lint:
	cd frontend && npm run lint && npm run typecheck
