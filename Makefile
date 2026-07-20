.PHONY: test integration-test vet build run migrate swagger swagger-check

test:
	go test ./...

integration-test:
	go test ./internal/infrastructure/postgres -count=1

vet:
	go vet ./...

build:
	go build ./cmd/server ./cmd/migrate ./cmd/openapi

run:
	go run ./cmd/server

migrate:
	go run ./cmd/migrate

swagger:
	go generate ./api

swagger-check:
	go test ./api
