BINARY = wakeup
BUILD_DIR = .
GO_FILES = $(shell find . -name '*.go' -not -path './worker/*')

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -ldflags "-X main.version=$(VERSION)"

.PHONY: build install uninstall clean test dev deploy-worker

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/wakeup/

test:
	go test ./... -v

install: build
	sudo ./$(BINARY) install

uninstall:
	sudo $(BINARY) uninstall

dev: build
	./$(BINARY) daemon

deploy-worker:
	cd worker && bun install && bun run deploy

clean:
	rm -f $(BUILD_DIR)/$(BINARY)
	rm -rf worker/node_modules worker/.wrangler
