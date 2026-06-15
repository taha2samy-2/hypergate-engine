BINARY_NAME=engine
BIN_DIR=bin

.PHONY: all build clean generate deps run

all: deps generate build

deps:
	go mod tidy

generate:
	buf generate buf.build/envoyproxy/envoy --path envoy/service/auth/v3

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/engine

run: build
	./$(BIN_DIR)/$(BINARY_NAME) -c config.yaml

clean:
	rm -rf $(BIN_DIR)
	rm -rf pkg/api