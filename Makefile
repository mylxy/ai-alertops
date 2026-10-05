.PHONY: build frontend backend runner demo-db test
build: frontend backend
frontend:
	cd frontend && npm ci && npm run build
backend:
	cd backend && go build -o bin/alertops ./cmd/alertops
runner:
	docker build -t alertops-runner:local runner
demo-db:
	docker compose -p alertops-demo -f deploy/compose.demo.yaml up -d --wait
test:
	cd backend && go test -tags=integration ./... -count=1 -timeout=180s
