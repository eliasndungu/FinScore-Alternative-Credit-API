PROTO_DIR    := proto/scoring
PROTO_FILE   := $(PROTO_DIR)/scoring.proto
GOPATH_BIN   := $(shell go env GOPATH)/bin

.PHONY: all proto build test lint clean docker-up docker-down

## all: generate proto stubs, then build.
all: proto build

## proto: regenerate Go and Python gRPC stubs from the .proto file.
proto:
	@echo "→ Generating Go protobuf stubs…"
	PATH="$$PATH:$(GOPATH_BIN)" protoc \
		--go_out=. \
		--go_opt=paths=source_relative \
		--go-grpc_out=. \
		--go-grpc_opt=paths=source_relative \
		$(PROTO_FILE)
	@echo "→ Generating Python protobuf stubs…"
	python3 -m grpc_tools.protoc \
		-I proto \
		--python_out=python-scorer \
		--grpc_python_out=python-scorer \
		$(PROTO_FILE)
	@echo "✓ Proto generation complete."

## build: compile the Go API binary.
build:
	@echo "→ Building Go API…"
	go build -ldflags="-s -w" -o bin/finscore-api ./cmd/api
	@echo "✓ Binary: bin/finscore-api"

## test: run the Go test suite.
test:
	go test -race -count=1 ./...

## lint: run golangci-lint (install if missing).
lint:
	@which golangci-lint > /dev/null 2>&1 || \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run ./...

## clean: remove build artefacts.
clean:
	rm -rf bin/

## docker-up: start all services with Docker Compose.
docker-up:
	docker compose up --build -d

## docker-down: stop all services.
docker-down:
	docker compose down -v
