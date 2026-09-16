.PHONY: check build test vet lint fmt fmt-check mod-check proto-check bin-check config-check model-collide model-collide-verify model-apply assets-pin model-scaffold bench bench-behavior run-gate run-gate-debug run-gate-debug-bvh run-gate-observe observe observe-down run-world run-demo run-tui tui-dump wasm-collide wasm-boss wasm-aggro wasm-throw serve-collide

check: fmt-check mod-check proto-check bin-check build test lint config-check

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

# 提交前检查：暂存区不应有构建产物/大文件（本项目已犯过四次，故自动化）。
bin-check:
	sh scripts/check-staged-binaries.sh

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

# 钉住资产版本（改了资产/模型之后跑；CI 按它检出资产仓）。
assets-pin:
	go run ./cmd/modelcollide -pin

# 给未登记模型生成候选清单条目（dry-run；确认后加 -yes 写进 configs/models.json）。
# 猜的是 shape/band/targets；scale 与 band 必须人工确认（note 里有 TODO 与推导结果）。
model-scaffold:
	go run ./cmd/modelcollide -scaffold

bench:
	go test -bench=. -benchmem -run '^$$' ./internal/ecs/
	cd actor && go test -bench=. -benchmem -run '^$$' .

# 行为树性能：纯树逻辑（behavior 包）+ 接入 ECS 后（world 包）。
# 前者看节点数的边际成本，后者看真实开销（含黑板取值与寻路等动作代价）。
bench-behavior:
	go test -bench=. -benchmem -run '^$$' ./internal/game/behavior/
	go test -bench BehaviorTree -benchmem -run '^$$' ./internal/game/world/

run-gate:
	go run ./cmd/gate

# 调试用：下发碰撞体线框（GATE_DEBUG_COLLISION）与 AOI 框（GATE_DEBUG_AOI），
# 并显式锁走 OrcaAOI 邻居来源，便于在 TUI/Godot 里核对三阶段移动。
# 邻居来源缺省本来就是 OrcaAOI，这里显式写上是为了让"调试跑的就是它"这件事可读；
# 想对照 BVH 用 make run-gate-debug-bvh。
run-gate-debug:
	GATE_DEBUG_COLLISION=1 \
	GATE_DEBUG_AOI=1 \
	GATE_NEIGHBOR_BACKEND=orca-aoi \
	go run ./cmd/gate

# 同上的调试模式，但邻居来源退回 BVH（性能/行为对照用）。
run-gate-debug-bvh:
	GATE_DEBUG_COLLISION=1 \
	GATE_DEBUG_AOI=1 \
	GATE_NEIGHBOR_BACKEND=bvh \
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

# Boss 行为树演示：把**真实 ECS + 行为树**编译成 WASM（boss.html）。
# 与 wasm-collide 分开编译：两者是不同的 main 包、不同的入口约定。
wasm-boss:
	GOOS=js GOARCH=wasm go build -o web/collide/boss.wasm ./cmd/bossdemo
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/collide/wasm_exec.js

# 群体仇恨演示：把**真实的仇恨传播机制**编译成 WASM（aggro.html）。
# 与 wasm-boss 同理，是独立的 main 包。
wasm-aggro:
	GOOS=js GOARCH=wasm go build -o web/collide/aggro.wasm ./cmd/aggrodemo
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/collide/wasm_exec.js

# 投掷/爆炸演示：把**真实的投掷机制**编译成 WASM（throw.html）。
wasm-throw:
	GOOS=js GOARCH=wasm go build -o web/collide/throw.wasm ./cmd/throwdemo
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/collide/wasm_exec.js

# 本地起个静态服务器预览可视化页面（wasm 需要走 http）。
serve-collide: wasm-collide wasm-boss wasm-aggro wasm-throw
	@echo "打开 http://localhost:8099/（Boss：/boss.html；群体仇恨：/aggro.html；投掷：/throw.html）"
	cd web/collide && python3 -m http.server 8099
