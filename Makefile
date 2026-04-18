# Antic-PT: Anticipation Protocol Makefile

.PHONY: help build test fmt lint run demo-server clean

help:
	@echo "Antic-PT Development Commands:"
	@echo "  build         Build the Spec-Link Go proxy"
	@echo "  test          Run Go unit tests"
	@echo "  fmt           Format Go and JavaScript code"
	@echo "  lint          Lint Go code"
	@echo "  run           Run the Spec-Link Go proxy (default config)"
	@echo "  demo-server   Run the Node.js reference demo server"
	@echo "  clean         Remove build artifacts"

build:
	cd spec-link && go build -o ../bin/spec-link main.go

test:
	cd spec-link && go test ./...

fmt:
	cd spec-link && go fmt ./...
	# Assuming prettier is installed for JS formatting
	# npx prettier --write "demo/**/*.js"

lint:
	cd spec-link && go vet ./...
	# If golangci-lint is installed:
	# golangci-lint run ./spec-link/...

run:
	cd spec-link && go run main.go

demo-server:
	cd demo/server && npm start

clean:
	rm -rf bin/
