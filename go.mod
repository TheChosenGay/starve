module starve

go 1.25.3

require (
	github.com/TheChosenGay/combet v0.0.0-20260814152537-a4f6fbc52642
	github.com/TheChosenGay/feeds/pkg v0.0.0-20260701065738-4d16e8fe401a
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/gorilla/websocket v1.5.3
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
)

// 复用本地 feeds 的 user 模块：登录 token 校验走 feeds/pkg/auth（同一 JWT_SECRET）。
// 部署到其他机器需要 feeds 仓库存在（或发布后去掉 replace）。
replace github.com/TheChosenGay/feeds/pkg => /Users/daishan/feeds/pkg
