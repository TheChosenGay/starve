#!/usr/bin/env bash
# run-gate.sh —— starve 网关的简单后台启停脚本（开发/演示用）。
#
# 用法：
#   scripts/run-gate.sh start     # 后台启动（日志 logs/gate.log，PID logs/gate.pid）
#   scripts/run-gate.sh stop      # 停止（SIGTERM，触发关服存档）
#   scripts/run-gate.sh restart
#   scripts/run-gate.sh status
#
# 配置（环境变量覆盖，均带默认值）：
#   GATE_WS_ADDR        监听地址，默认 :8081
#   GATE_TICK_MS        tick 毫秒，默认 100
#   GATE_SAVE_FILE      存档文件，默认 data/save.bin
#   GATE_RESOURCES      资源配置表，默认 configs/resources.json
#   GATE_HUNGER_RATE    饥饿速率/每tick，默认 0（不消耗）
#   GATE_OFFLINE_SECONDS 离线保留秒数，默认 300
set -euo pipefail

cd "$(dirname "$0")/.."

GATE_WS_ADDR="${GATE_WS_ADDR:-:8081}"
GATE_TICK_MS="${GATE_TICK_MS:-100}"
GATE_SAVE_FILE="${GATE_SAVE_FILE:-data/save.bin}"
GATE_RESOURCES="${GATE_RESOURCES:-configs/resources.json}"
GATE_HUNGER_RATE="${GATE_HUNGER_RATE:-0}"
GATE_OFFLINE_SECONDS="${GATE_OFFLINE_SECONDS:-300}"

BIN=bin/gate
LOG_DIR=logs
PID_FILE=logs/gate.pid
mkdir -p "$LOG_DIR" data

build() {
  echo "构建 $BIN ..."
  go build -o "$BIN" ./cmd/gate
}

start() {
  if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "已在运行（PID $(cat "$PID_FILE")）"
    return 0
  fi
  build
  export GATE_WS_ADDR GATE_TICK_MS GATE_SAVE_FILE GATE_RESOURCES GATE_HUNGER_RATE GATE_OFFLINE_SECONDS
  nohup "$BIN" >> "$LOG_DIR/gate.log" 2>&1 &
  echo $! > "$PID_FILE"
  echo "已启动（PID $(cat "$PID_FILE")），日志 $LOG_DIR/gate.log"
}

stop() {
  if [ ! -f "$PID_FILE" ]; then
    echo "没有 PID 文件，未运行"
    return 0
  fi
  PID=$(cat "$PID_FILE")
  if kill -0 "$PID" 2>/dev/null; then
    echo "发送停止信号（PID $PID），等待退出（触发关服存档）..."
    kill "$PID"
    for _ in $(seq 1 40); do
      if ! kill -0 "$PID" 2>/dev/null; then
        break
      fi
      sleep 0.5
    done
  fi
  rm -f "$PID_FILE"
  echo "已停止"
}

status() {
  if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "运行中（PID $(cat "$PID_FILE")）"
  else
    echo "未运行"
  fi
}

case "${1:-start}" in
  start) start ;;
  stop) stop ;;
  restart) stop; start ;;
  status) status ;;
  *) echo "用法: $0 {start|stop|restart|status}" >&2; exit 1 ;;
esac
