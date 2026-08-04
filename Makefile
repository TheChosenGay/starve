.PHONY: build test vet lint fmt run-gate run-world

build:
	go build ./...

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint 未安装，降级用 go vet"; \
		go vet ./...; \
	fi

fmt:
	gofmt -l -w .

run-gate:
	go run ./cmd/gate

run-world:
	go run ./cmd/world
