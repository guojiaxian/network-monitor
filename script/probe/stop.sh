#!/usr/bin/env bash
# NetWatch 探针端 —— 停止脚本（在探针主机上运行）
# 优雅停止 netwatch-agent；身份与离线缓存保留在 /var/lib/netwatch-agent。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=script/probe/lib.sh
. "$SCRIPT_DIR/lib.sh"

usage() {
  cat <<'EOF'
用法: stop.sh [选项]

停止探针 netwatch-agent（systemd 托管，身份与缓存目录保留）。

选项:
  -h, --help    显示本帮助

环境变量:
  NW_AGENT_SERVICE     服务单元名（默认 netwatch-agent）
  NW_AGENT_STOP_TIMEOUT 等待 inactive 的秒数（默认 20）
  NO_COLOR=1           关闭彩色输出
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    *) ag_err "未知参数：$1"; usage; exit 2 ;;
  esac
  shift
done

ag_validate_config
ag_require_root
ag_require_cmd systemctl
ag_lock

if ! ag_stop; then
  ag_err "停止未完成。排查：journalctl -u $NW_AGENT_SERVICE -n 100 --no-pager"
  exit 1
fi
ag_ok "探针已停止"
