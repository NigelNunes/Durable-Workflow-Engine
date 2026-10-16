.PHONY: up down migrate test lint demo bench

DATABASE_URL ?= postgres://durable:durable@localhost:5433/durable?sslmode=disable

up:
	docker compose up -d --wait

down:
	docker compose down

migrate:
	DATABASE_URL='$(DATABASE_URL)' go run ./cmd/migrate

test:
	DATABASE_URL='$(DATABASE_URL)' go test -p 1 -timeout 40m ./...

lint:
	go vet ./...

demo:
	docker compose --profile demo up -d --build --wait

# Phase 12 load generator. Postgres must be up (make up && make migrate).
# Writes markdown metrics to stdout; redirect into docs/benchmarks.md after the header.
bench:
	DATABASE_URL='$(DATABASE_URL)' go run ./bench -mode=all -lease-ttl=1s
