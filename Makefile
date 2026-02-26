BINARY      := gateway-pro
CMD         := ./cmd/gateway
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
               -X main.version=$(VERSION) \
               -X main.commit=$(COMMIT) \
               -X main.buildTime=$(BUILD_TIME)

.PHONY: all build run test lint fmt vet docker clean help

all: build

## build: Compile the binary
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

## run: Run with the default config
run: build
	./bin/$(BINARY) -config configs/gateway.yaml

## test: Run all tests with race detector
test:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

## test-integration: Run integration tests (requires running backends)
test-integration:
	go test -race -tags=integration ./tests/integration/...

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## fmt: Format source
fmt:
	gofmt -s -w .
	goimports -w .

## vet: Run go vet
vet:
	go vet ./...

## docker-build: Build Docker image
docker-build:
	docker build -f deploy/docker/Dockerfile -t $(BINARY):$(VERSION) -t $(BINARY):latest .

## docker-push: Push image to Docker Hub
docker-push: docker-build
	docker push snehakhoreja/$(BINARY):$(VERSION)
	docker push snehakhoreja/$(BINARY):latest

## docker-compose-up: Start full stack with docker-compose
docker-compose-up:
	docker compose -f examples/docker-compose/docker-compose.yaml up --build

## docker-compose-down: Tear down
docker-compose-down:
	docker compose -f examples/docker-compose/docker-compose.yaml down

## bench: Run benchmarks
bench:
	go test -bench=. -benchmem ./...

## load-test: Quick load test with hey (requires: go install github.com/rakyll/hey@latest)
load-test:
	hey -n 50000 -c 100 http://localhost:8080/

## clean: Remove build artifacts
clean:
	rm -rf bin/ coverage.out

## help: Show this help
help:
	@grep -E '^## ' Makefile | sed 's/## //'
