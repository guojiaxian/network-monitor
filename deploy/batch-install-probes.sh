#!/usr/bin/env bash
# 探针批量安装：按 CSV 清单逐台分发并安装（含探针名称，注册后后台直接显示）。
#
# probes.csv 每行：IP,SSH用户,名称[,端口]（默认 22，# 开头为注释）
# 用法：
#   ./batch-install-probes.sh probes.csv <中心地址> <注册令牌> [探针二进制]
# 示例：
#   ./batch-install-probes.sh probes.csv http://10.8.0.10:8080 s3cret-token ../bin/netwatch-agent
# 前置：本机可 SSH 免密登录各探针 VM；二进制缺省则从交付包目录自动获取。
set -euo pipefail

LIST="${1:-}"
SERVER="${2:-}"
TOKEN="${3:-}"
BIN="${4:-}"

[[ -n "$LIST" && -n "$SERVER" && -n "$TOKEN" ]] || { echo "用法: $0 probes.csv <中心地址> <令牌> [二进制]"; exit 1; }
[[ -f "$LIST" ]] || { echo "清单不存在: $LIST"; exit 1; }
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$(dirname "$SCRIPT_DIR")/bin"

FAIL=0
while IFS=, read -r IP USER NAME PORT; do
  [[ -z "$IP" || "$IP" =~ ^# ]] && continue
  USER=${USER:-root}; PORT=${PORT:-22}; NAME=${NAME:-}
  echo "==> $IP（${NAME:-未命名}）"
  SSH_OPTS=(-o StrictHostKeyChecking=no -o ConnectTimeout=8 -p "$PORT")
  if ! ssh "${SSH_OPTS[@]}" "$USER@$IP" 'true' 2>/dev/null; then
    echo "    SSH 连接失败，跳过"; FAIL=1; continue
  fi
  if [[ -n "$BIN" && -f "$BIN" ]]; then
    scp -q -P "$PORT" "$BIN" "$SCRIPT_DIR/install-agent.sh" "$USER@$IP:/tmp/"
    ssh "${SSH_OPTS[@]}" "$USER@$IP" "bash /tmp/install-agent.sh $SERVER $TOKEN /tmp/$(basename "$BIN") '$NAME'" | tail -2
  elif [[ -f "$BIN_DIR/netwatch-agent" ]]; then
    scp -q -P "$PORT" "$BIN_DIR/netwatch-agent" "$SCRIPT_DIR/install-agent.sh" "$USER@$IP:/tmp/"
    ssh "${SSH_OPTS[@]}" "$USER@$IP" "bash /tmp/install-agent.sh $SERVER $TOKEN /tmp/netwatch-agent '$NAME'" | tail -2
  else
    scp -q -P "$PORT" "$SCRIPT_DIR/install-agent.sh" "$USER@$IP:/tmp/"
    ssh "${SSH_OPTS[@]}" "$USER@$IP" "bash /tmp/install-agent.sh $SERVER $TOKEN" | tail -2
  fi
  echo "    完成"
done < "$LIST"

[[ $FAIL -eq 0 ]] && echo "全部完成" || { echo "存在失败项，请检查上方日志"; exit 1; }
