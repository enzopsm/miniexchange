.PHONY: build run test lint

build:
	go build -o miniexchange ./cmd/miniexchange

run: build
	./miniexchange

test:
	go test -race -count=1 ./...

lint:
	goimports -l .
	go vet ./...
