.PHONY: check build test vet lint fmt fmt-check mod-check proto-check config-check bench run-gate run-gate-observe observe observe-down run-world run-demo wasm-collide serve-collide

check: fmt-check mod-check proto-check build test lint config-check

build:
	go build ./...
	cd actor && go build ./...

test:
	go test -race ./...
	cd actor && go test -race ./...

vet:
	go vet ./...
	cd actor && go vet ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
		cd actor && golangci-lint run ./...; \
	else \
		echo "golangci-lint 未安装，降级用 go vet"; \
		go vet ./...; \
		cd actor && go vet ./...; \
	fi

fmt:
	gofmt -l -w .
	cd actor && gofmt -l -w .

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "以下 Go 文件需要 gofmt:"; \
		echo "$$files"; \
		exit 1; \
	fi
	@files="$$(cd actor && gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "以下 actor 模块文件需要 gofmt:"; \
		echo "$$files"; \
		exit 1; \
	fi

mod-check:
	go mod tidy -diff
	cd actor && go mod tidy -diff

proto-check:
	sh scripts/check_generated_proto.sh

config-check:
	go run ./cmd/configcheck

bench:
	go test -bench=. -benchmem -run '^$$' ./internal/ecs/
	cd actor && go test -bench=. -benchmem -run '^$$' .

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

# 把 pkg/collide 编译成浏览器可视化用的 WebAssembly（方案 A）。
wasm-collide:
	GOOS=js GOARCH=wasm go build -o web/collide/collide.wasm ./cmd/collidewasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/collide/wasm_exec.js

# 本地起个静态服务器预览可视化页面（wasm 需要走 http）。
serve-collide: wasm-collide
	@echo "打开 http://localhost:8099/"
	cd web/collide && python3 -m http.server 8099
