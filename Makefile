SHELL := /bin/sh
COMPOSE ?= docker compose

.PHONY: bootstrap generate build test up down simulate fmt lint infra

bootstrap:
	@command -v buf >/dev/null || (echo "Buf is required: https://buf.build/docs/installation" && exit 1)
	buf dep update
	$(MAKE) generate

generate:
	buf generate

build: generate
	cmake -S simulator/evse-simulator -B build/evse-simulator
	cmake --build build/evse-simulator
	cargo build --manifest-path edge/orvolt-edge-agent/Cargo.toml --locked
	cd cloud/control-plane && go build ./cmd/control-plane

test: build
	ctest --test-dir build/evse-simulator --output-on-failure
	cargo test --manifest-path edge/orvolt-edge-agent/Cargo.toml --locked
	cd cloud/control-plane && go test ./...

up:
	$(COMPOSE) up --build

infra:
	$(COMPOSE) up -d postgres nats mosquitto

simulate:
	$(COMPOSE) up --build evse-simulator

down:
	$(COMPOSE) down --remove-orphans

fmt:
	cargo fmt --manifest-path edge/orvolt-edge-agent/Cargo.toml --check
	go fmt ./cloud/control-plane/...

lint:
	cargo clippy --manifest-path edge/orvolt-edge-agent/Cargo.toml --all-targets -- -D warnings
	go vet ./cloud/control-plane/...
