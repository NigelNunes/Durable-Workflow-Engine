.PHONY: up down migrate test lint

DATABASE_URL ?= postgres://durable:durable@localhost:5433/durable?sslmode=disable

up:
	docker compose up -d --wait

down:
	docker compose down

migrate:
	DATABASE_URL='$(DATABASE_URL)' go run ./cmd/migrate

test:
	DATABASE_URL='$(DATABASE_URL)' go test ./...

lint:
	go vet ./...
