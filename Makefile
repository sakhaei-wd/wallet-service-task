.PHONY: test integration-test vet build run migrate

test:
	go test ./...

integration-test:
	go test ./internal/infrastructure/postgres -run TestConcurrent -count=1

vet:
	go vet ./...

build:
	go build ./cmd/server ./cmd/migrate

run:
	go run ./cmd/server

migrate:
	go run ./cmd/migrate
