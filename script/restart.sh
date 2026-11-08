#!/usr/bin/env bash
# NetWatch 中心端 —— 重启脚本
# 默认整套重启（服务 -> 时序库 -> 数据库 停止，再按 数据库 -> 时序库 -> 服务 启动）；
# --app-only 用于仅重启 netwatch-server（升级二进制后的常用方式）。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=script/lib.sh
. "$SCRIPT_DIR/lib.sh"

usage() {
  cat <<'EOF'
用法: script/restart.sh [选项]

重启 NetWatch 中心端。默认整套重启：
  停止 netwatch-server -> VictoriaMetrics -> PostgreSQL
  启动 PostgreSQL -> VictoriaMetrics -> netwatch-server
每步均等待依赖/健康检查就绪；任一步失败立即中止。

选项:
  --app-only     只重启 netwatch-server（依赖保持运行；升级二进制推荐）
  --no-wait      启动后不等待依赖与健康检查
  --timeout N    每个依赖/健康检查的等待秒数（默认 60）
  -h, --help     显示本帮助

环境变量: 同 start.sh / stop.sh
  NW_SERVICE / NW_PG_SERVICE / NW_VM_SERVICE / NW_HEALTH_URL
  NW_START_TIMEOUT / NW_STOP_TIMEOUT / NO_COLOR

示例:
  sudo script/restart.sh                # 整套重启
  sudo script/restart.sh --app-only     # 仅重启中心服务（秒级，探针自动补传）
EOF
}

APP_ONLY=0
WAIT=1
while [ $# -gt 0 ]; do
  case "$1" in
    --app-only) APP_ONLY=1 ;;
    --no-wait)  WAIT=0 ;;
    --timeout)
      [ $# -ge 2 ] || nw_die "--timeout 需要一个整数秒参数"
      NW_START_TIMEOUT="$2"
      shift
      ;;
    -h|--help) usage; exit 0 ;;
    *) nw_err "未知参数：$1"; usage; exit 2 ;;
  esac
  shift
done

nw_validate_config
nw_require_root
nw_require_cmd systemctl
nw_require_cmd curl
nw_lock

if ! nw_stop_stack "$APP_ONLY"; then
  nw_err "停止阶段失败，已中止重启（未执行启动）"
  exit 1
fi
if ! nw_start_stack "$APP_ONLY" "$WAIT"; then
  nw_err "启动阶段失败。排查：journalctl -u $NW_SERVICE -n 100 --no-pager"
  exit 1
fi

nw_status
nw_print_access
nw_ok "中心端重启完成"
