.PHONY: build test check check-web-dist web-build web-dev

BINDIR ?= $(HOME)/go/bin

build: check-web-dist
	mkdir -p "$(BINDIR)"
	go build -trimpath -o "$(BINDIR)/paw" ./cmd/paw

check-web-dist:
	./scripts/check-web-dist.sh

test:
	go test ./... -count=1

check:
	go vet ./...
	go build ./...

web-build:
	./scripts/build-web.sh

web-dev:
	./scripts/dev-web.sh
