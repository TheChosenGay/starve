#!/bin/sh
set -eu

command -v protoc >/dev/null
command -v protoc-gen-go >/dev/null

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

protoc \
  --go_out="$tmp" \
  --go_opt=paths=source_relative \
  pkg/proto/message.proto \
  pkg/proto/game/game.proto

cmp "$tmp/pkg/proto/message.pb.go" pkg/proto/message.pb.go
cmp "$tmp/pkg/proto/game/game.pb.go" pkg/proto/game/game.pb.go
echo "generated protobuf code is current"
