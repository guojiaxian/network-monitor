#!/usr/bin/env bash
# NetWatch 探针端 —— 启动脚本（在探针主机上运行）
# 确保 netwatch-agent 开机自启并启动，等待服务进入 active。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=script/probe/lib.sh
. "$SCRIPT_DIR/lib.sh"

usage() {
  cat <<'EOF'
用法: start.sh [选项]

在探针主机上启动 netwatch-agent，并确保已设置开机自启（systemctl enable）。

选项:
  --no-wait     发起启动后不等待 active
  --timeout N   等待 active 的秒数（默认 30）
  -h, --help    显示本帮助

环境变量:
  NW_AGENT_SERVICE       服务单元名（默认 netwatch-agent）
  NW_AGENT_BIN           二进制路径（默认 /usr/local/bin/netwatch-agent）
  NW_AGENT_STATE_DIR     身份/缓存目录（默认 /var/lib/netwatch-agent）
  NO_COLOR=1             关闭彩色输出
EOF
}

WAIT=1
while [ $# -gt 0 ]; do
  case "$1" in
    --no-wait) WAIT=0 ;;
    --timeout)
      [ $# -ge 2 ] || ag_die "--timeout 需要一个整数秒参数"
      NW_AGENT_START_TIMEOUT="$2"
      shift
      ;;
    -h|--help) usage; exit 0 ;;
    *) ag_err "未知参数：$1"; usage; exit 2 ;;
  esac
  shift
done

ag_validate_config
ag_require_root
ag_require_cmd systemctl
ag_lock

if ! ag_start "$WAIT"; then
  ag_err "启动未完成。排查：journalctl -u $NW_AGENT_SERVICE -n 100 --no-pager"
  exit 1
fi
ag_status || true
ag_ok "探针启动完成"
