SHELL := /bin/sh
MIGRATE_VERSION ?= v4.18.1
MIGRATE_IMAGE := migrate/migrate:$(MIGRATE_VERSION)
DATABASE_URL ?= postgres://telegram:telegram@localhost:5432/telegram_server?sslmode=disable

.PHONY: run test lint vet build migrate-up migrate-down seed-dev psql compose-up compose-down clean smoke

run:
	go run ./cmd/server

build:
	go build -o bin/telegram_server ./cmd/server

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

migrate-up:
	docker run --rm --network host -v "$(CURDIR)/migrations:/migrations" \
		$(MIGRATE_IMAGE) -path /migrations -database "$(DATABASE_URL)" up

migrate-down:
	docker run --rm --network host -v "$(CURDIR)/migrations:/migrations" \
		$(MIGRATE_IMAGE) -path /migrations -database "$(DATABASE_URL)" down 1

seed-dev:
	docker run --rm --network host -v "$(CURDIR)/migrations:/migrations" \
		$(MIGRATE_IMAGE) -path /migrations -database "$(DATABASE_URL)" goto 2

psql:
	docker run --rm -it --network host -e PGPASSWORD=telegram postgres:16-alpine \
		psql -h localhost -U telegram -d telegram_server

compose-up:
	docker compose up -d

compose-down:
	docker compose down

smoke:
	./scripts/smoke.sh

clean:
	rm -rf bin/
