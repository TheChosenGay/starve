.PHONY: build test vet lint fmt bench run-gate run-world run-demo

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

bench:
	go test -bench=. -benchmem -run '^$$' ./internal/actor/ ./internal/ecs/

run-gate:
	go run ./cmd/gate

run-world:
	go run ./cmd/world

run-demo:
	go run ./cmd/ecsdemo
