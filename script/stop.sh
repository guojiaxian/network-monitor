#!/usr/bin/env bash
# NetWatch 中心端 —— 停止脚本
# 默认按启动的逆序停止：netwatch-server -> VictoriaMetrics -> PostgreSQL。
# 已停止的服务会跳过；停止后等待单元进入 inactive。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=script/lib.sh
. "$SCRIPT_DIR/lib.sh"

usage() {
  cat <<'EOF'
用法: script/stop.sh [选项]

停止 NetWatch 中心端。默认停止整套服务（netwatch-server -> VictoriaMetrics -> PostgreSQL），
先优雅停止中心服务（SIGTERM，最多 10 秒），再停止依赖，确保无写入中断。

选项:
  --app-only     只停止 netwatch-server，PostgreSQL/VictoriaMetrics 保持运行
  -h, --help     显示本帮助

环境变量:
  NW_SERVICE       中心服务单元名（默认 netwatch-server）
  NW_PG_SERVICE    PostgreSQL 单元名（默认 postgresql；置空=跳过）
  NW_VM_SERVICE    VictoriaMetrics 单元名（默认 victoriametrics；置空=跳过）
  NW_STOP_TIMEOUT  停止后等待 inactive 的秒数（默认 30）
  NO_COLOR=1       关闭彩色输出

示例:
  sudo script/stop.sh                  # 关闭整套中心端
  sudo script/stop.sh --app-only       # 仅停中心服务（保留数据库/时序库）
EOF
}

APP_ONLY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --app-only) APP_ONLY=1 ;;
    -h|--help) usage; exit 0 ;;
    *) nw_err "未知参数：$1"; usage; exit 2 ;;
  esac
  shift
done

nw_validate_config
nw_require_root
nw_require_cmd systemctl
nw_lock

if ! nw_stop_stack "$APP_ONLY"; then
  nw_err "停止未完成。排查：journalctl -u $NW_SERVICE -n 100 --no-pager"
  exit 1
fi

nw_status
nw_ok "中心端已关闭"
