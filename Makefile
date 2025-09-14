.PHONY: help run test build deploy port-forward

help:
	@echo "Available commands:"
	@echo "  make run service=gateway  - Run a service locally"
	@echo "  make test                 - Run all tests"
	@echo "  make build service=gateway- Build a service"
	@echo "  make port-forward         - Forward GKE services to local"
	@echo "  make deploy              - Deploy to GKE (via GitHub Actions)"

run:
	cd services/$(service) && go run main.go

test:
	go test ./...

build:
	cd services/$(service) && go build -o bin/$(service) main.go

port-forward:
	./scripts/port-forward.sh

deploy:
	git add . && git commit -m "Deploy" && git push origin main
