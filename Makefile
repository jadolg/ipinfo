IMAGE   ?= ghcr.io/jadolg/ipinfo
TAG     ?= latest
BINARY  ?= ipinfo

BUILD_FLAGS := -trimpath -ldflags="-s -w"

.PHONY: all build test test-race bench lint docker-build docker-push up down clean

all: test-all build

build:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY) .

test:
	go test ./...

test-race:
	go test -race ./...

bench:
	go test -run='^$$' -bench=$(or $(BENCH),.) -benchmem -benchtime=3s ./...

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY)

test-all: test test-race bench
