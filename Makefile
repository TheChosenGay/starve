.PHONY: check build test vet lint fmt fmt-check mod-check proto-check config-check model-collide model-collide-verify model-apply bench run-gate run-gate-observe observe observe-down run-world run-demo run-tui tui-dump wasm-collide serve-collide

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
	go run ./cmd/modelcollide -check

# 模型 → 服务端简化碰撞体：改了客户端模型或 configs/models.json 之后跑这个。
model-collide:
	go run ./cmd/modelcollide -v -write
	go run ./cmd/modelcollide -check

# 重新读模型推导，与生成文件对比 + 自动验收（抓"改了模型忘了跑流水线"/形状与模型不符）。
model-collide-verify:
	go run ./cmd/modelcollide -verify -strict

# 把手写配置同步成推导值（默认 dry-run；确认 diff 后加 -yes）。
model-apply:
	go run ./cmd/modelcollide -apply

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

# 终端客户端：不开 Godot 也能连服务器走路/看碰撞体。
# 先在**另一个终端**跑 make run-gate 起服务端（-uid 换成自己的）。
run-tui:
	go run ./cmd/tui -uid 42

# 冒烟：连上本地网关，收一帧快照打印出来就退出（不需要 TTY，可进 CI）
tui-dump:
	go run ./cmd/tui -uid 42 -dump -no-color

# 把 pkg/collide 编译成浏览器可视化用的 WebAssembly（方案 A）。
wasm-collide:
	GOOS=js GOARCH=wasm go build -o web/collide/collide.wasm ./cmd/collidewasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/collide/wasm_exec.js

# 本地起个静态服务器预览可视化页面（wasm 需要走 http）。
serve-collide: wasm-collide
	@echo "打开 http://localhost:8099/"
	cd web/collide && python3 -m http.server 8099
