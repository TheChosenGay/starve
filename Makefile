.PHONY: check build test vet lint fmt fmt-check mod-check proto-check config-check bench run-gate run-gate-observe observe observe-down run-world run-demo

check: fmt-check mod-check proto-check build test lint config-check

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

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "以下 Go 文件需要 gofmt:"; \
		echo "$$files"; \
		exit 1; \
	fi

mod-check:
	go mod tidy -diff

proto-check:
	sh scripts/check_generated_proto.sh

config-check:
	go run ./cmd/configcheck

bench:
	go test -bench=. -benchmem -run '^$$' ./internal/actor/ ./internal/ecs/

run-gate:
	go run ./cmd/gate

# 给 Docker 里的 Prometheus 抓取：指标口绑到 0.0.0.0，不再只监听 127.0.0.1。
run-gate-observe:
	GATE_METRICS_ADDR=0.0.0.0:9090 go run ./cmd/gate

observe:
	docker compose -f deploy/observe/docker-compose.yml up -d

observe-down:
	docker compose -f deploy/observe/docker-compose.yml down

run-world:
	go run ./cmd/world

run-demo:
	go run ./cmd/ecsdemo
