VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build dist run test clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gamepanel ./cmd/gamepanel

dist:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gamepanel-linux-amd64 ./cmd/gamepanel
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gamepanel-linux-arm64 ./cmd/gamepanel
	cp scripts/install.sh dist/install.sh

run: build
	GP_DATA=./devdata GP_LISTEN=127.0.0.1:8420 ./dist/gamepanel

test:
	go vet ./... && go test ./...

clean:
	rm -rf dist devdata
