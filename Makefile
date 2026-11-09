VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test race lint fuzz bench golden snapshot clean

build:
	go build -ldflags "$(LDFLAGS)" -o spool ./cmd/spool

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run

fuzz:
	go test ./internal/domain/signature/stripe -run=^$$ -fuzz=FuzzParseHeader -fuzztime=30s

bench:
	go test ./internal/bootstrap -run=^$$ -bench=. -benchmem

golden:
	go test ./internal/domain/diagnose -update

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf spool dist
