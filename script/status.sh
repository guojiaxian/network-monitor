#!/usr/bin/env bash
# NetWatch 中心端 —— 状态查看脚本（只读，不改变服务状态）
# 展示三个单元的 active/开机自启/PID 与 /api/health 输出。
# 退出码：0=中心服务 active 且 health=ok；1=未运行或健康检查未通过。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=script/lib.sh
. "$SCRIPT_DIR/lib.sh"

usage() {
  cat <<'EOF'
用法: script/status.sh [选项]

查看 NetWatch 中心端状态（PostgreSQL / VictoriaMetrics / netwatch-server 及 /api/health）。
只读操作，无需 root。

选项:
  -h, --help     显示本帮助

退出码:
  0  中心服务运行中且健康检查 status=ok
  1  中心服务未运行，或健康检查未通过（degraded/不可达）

环境变量:
  NW_SERVICE / NW_PG_SERVICE / NW_VM_SERVICE / NW_HEALTH_URL
  NO_COLOR=1     关闭彩色输出
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    *) nw_err "未知参数：$1"; usage; exit 2 ;;
  esac
  shift
done

nw_validate_config
nw_require_cmd systemctl

nw_status
nw_print_access

if nw_is_active "$NW_SERVICE" && nw_server_ready; then
  nw_ok "中心服务运行正常"
  exit 0
fi

nw_err "中心服务未就绪（未运行或健康检查非 ok）"
printf '  启动：sudo %s/start.sh\n' "$SCRIPT_DIR"
printf '  日志：journalctl -u %s -n 100 --no-pager\n' "$NW_SERVICE"
exit 1
