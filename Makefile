.PHONY: build test lint run

build:
	go build -o bin/extractor ./cmd/extractor

test:
	go test ./... -race

lint:
	gofmt -w cmd internal
	go vet ./...

run:
	go run ./cmd/extractor $(ARGS)
