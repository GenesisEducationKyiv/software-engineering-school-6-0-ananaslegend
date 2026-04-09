BINARY          = bin/api
MIGRATIONS_PATH = ./migrations
DB_URL         ?= postgres://postgres:pass@localhost:5432/postgres?sslmode=disable

.PHONY: build run test vet generate tidy lint migrate-up migrate-down clean

build:
	go build -o $(BINARY) ./cmd/api

run:
	go run ./cmd/api

test:
	go test ./...

vet:
	go vet ./...

gen:
	go generate ./...

tidy:
	go mod tidy

lint:
	golangci-lint run ./...

migrate-up:
	migrate -path $(MIGRATIONS_PATH) -database "$(DB_URL)" up

migrate-down:
	migrate -path $(MIGRATIONS_PATH) -database "$(DB_URL)" down 1

clean:
	rm -rf bin/

docker-up:
	docker compose up --build

docker-down:
	docker compose down

docker-clean:
	docker compose down -v
