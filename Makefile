# Optional convenience targets. None are required: every target is a plain `go`
# command you can run directly (see README). Windows users do not need `make`.

.PHONY: build vet test test-race test-live

build:
	go build ./...

vet:
	go vet ./...

# Primary, deterministic, cross-platform test suite (no public network).
test:
	go test ./...

# Race detector. Requires CGO_ENABLED=1 and a C compiler; authoritative in Linux CI.
test-race:
	go test -race ./...

# Live tests that contact public DNS/SMTP/HTTP/provider services (excluded by default).
test-live:
	go test -tags=live ./...
