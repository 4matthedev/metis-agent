GO ?= go

.PHONY: all build test race cover vet fmt fmtcheck check clean

all: check

build:
	$(GO) build ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

fmtcheck:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt (run 'make fmt'):"; echo "$$unformatted"; exit 1; \
	fi

check: fmtcheck vet build test

clean:
	rm -f coverage.out
