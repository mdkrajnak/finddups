BINARY := finddups
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build build-arm64 test test-race clean

build:
	go build $(LDFLAGS) -o $(BINARY) .

build-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-arm64 .

test:
	go test ./...

test-race:
	go test -race ./...

clean:
	rm -f $(BINARY) $(BINARY)-arm64 finddups.db
