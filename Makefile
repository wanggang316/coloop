BINARY := coloop
PKG := ./cmd/coloop
LDFLAGS := -s -w

.PHONY: build test race vet fmt run clean dist install

## build: compile the binary into ./bin
build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

## test: run unit + integration tests
test:
	go test ./...

## race: run the full suite under the race detector
race:
	go test -race -count=1 ./...

## vet: static analysis
vet:
	go vet ./...

## fmt: format all Go sources
fmt:
	gofmt -w .

## run: build and start the daemon
run: build
	./bin/$(BINARY) serve

## clean: remove build artifacts
clean:
	rm -rf bin dist

## dist: cross-compiled release binaries for macOS + Linux (amd64/arm64)
dist: clean
	@mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 $(PKG)
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 $(PKG)
	GOOS=linux  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 $(PKG)
	GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 $(PKG)

## install: install the binary into ~/.local/bin
install: build
	@mkdir -p $(HOME)/.local/bin
	install -m 0755 bin/$(BINARY) $(HOME)/.local/bin/$(BINARY)
