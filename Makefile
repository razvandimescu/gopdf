.PHONY: all build test fmt vet cyclo ineffassign misspell staticcheck govulncheck lint check

all: check

build:
	go build ./...

test:
	go test -race ./...

fmt:
	@output=$$(gofmt -s -d .); \
	if [ -n "$$output" ]; then echo "$$output"; exit 1; fi

vet:
	go vet ./...

cyclo:
	@output=$$(gocyclo -over 41 . | grep -v 'text.go:'); \
	if [ -n "$$output" ]; then echo "$$output"; exit 1; fi

ineffassign:
	ineffassign ./...

misspell:
	misspell -error .

staticcheck:
	staticcheck ./...

govulncheck:
	govulncheck ./...

lint: fmt vet cyclo ineffassign misspell staticcheck govulncheck

check: build test lint
