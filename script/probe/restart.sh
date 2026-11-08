#!/usr/bin/env bash
# NetWatch 探针端 —— 重启脚本（在探针主机上运行）
# 先停止再启动 netwatch-agent，并等待 active；身份与缓存保留。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=script/probe/lib.sh
. "$SCRIPT_DIR/lib.sh"

usage() {
  cat <<'EOF'
用法: restart.sh [选项]

重启探针 netwatch-agent（停止 -> 启动 -> 等待 active）。

选项:
  --no-wait     启动后不等待 active
  --timeout N   等待 active 的秒数（默认 30）
  -h, --help    显示本帮助

环境变量: 同 start.sh / stop.sh
  NW_AGENT_SERVICE / NW_AGENT_BIN / NW_AGENT_STATE_DIR
  NW_AGENT_START_TIMEOUT / NW_AGENT_STOP_TIMEOUT / NO_COLOR
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

if ! ag_restart "$WAIT"; then
  ag_err "重启未完成。排查：journalctl -u $NW_AGENT_SERVICE -n 100 --no-pager"
  exit 1
fi
ag_status || true
ag_ok "探针重启完成"
