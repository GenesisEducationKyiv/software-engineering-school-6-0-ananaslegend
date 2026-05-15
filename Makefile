BINARY                = bin/api
MIGRATIONS_PATH       = ./migrations
DB_URL               ?= postgres://postgres:pass@localhost:5432/postgres?sslmode=disable
GOLANGCI_LINT_VERSION = latest
SWAG_VERSION          = v1.16.6

.PHONY: build run test vet generate tidy mod-update mod-update-patch lint lint-install lint-fix fix fix-diff migrate-up migrate-down clean swagger swagger-install test-integration test-e2e test-all

build:
	go build -mod=vendor -o $(BINARY) ./cmd/api

run:
	go run ./cmd/api

test:
	go test ./...

test-integration:
	go test -tags=integration -count=1 ./tests/integration/...

test-e2e:
	go test -tags=e2e -count=1 ./tests/e2e/...

test-all:
	go test -tags=integration -count=1 ./...

gen:
	go generate ./...

tidy:
	go mod tidy
	go mod vendor

lint-install:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint:
	golangci-lint run ./...

lint-fix:
	go fix ./...
	golangci-lint run --fix ./...

migrate-up:
	migrate -path $(MIGRATIONS_PATH) -database "$(DB_URL)" up

migrate-down:
	migrate -path $(MIGRATIONS_PATH) -database "$(DB_URL)" down 1

swagger-install:
	go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)

swagger:
	swag init -g cmd/api/main.go -o docs/swagger --parseDependency

clean:
	rm -rf bin/

docker-up:
	docker compose up --build

docker-down:
	docker compose down

docker-clean:
	docker compose down -v
