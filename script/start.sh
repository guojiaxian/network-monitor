#!/usr/bin/env bash
# NetWatch 中心端 —— 启动脚本
# 默认按依赖顺序启动：PostgreSQL -> VictoriaMetrics -> netwatch-server，并等待健康检查通过。
# 所有单元未启用开机自启时会自动 systemctl enable。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=script/lib.sh
. "$SCRIPT_DIR/lib.sh"

usage() {
  cat <<'EOF'
用法: script/start.sh [选项]

启动 NetWatch 中心端。默认启动整套服务（PostgreSQL -> VictoriaMetrics -> netwatch-server），
并依次等待数据库可连接、时序库健康、中心服务 /api/health 返回 ok。
已运行的服务会跳过；未设置开机自启的单元会自动 enable。

选项:
  --app-only     只启动 netwatch-server（假定 PostgreSQL/VictoriaMetrics 已在运行）
  --no-wait      只发起启动，不等待依赖与健康检查
  --timeout N    每个依赖/健康检查的等待秒数（默认 60）
  -h, --help     显示本帮助

环境变量:
  NW_SERVICE       中心服务单元名（默认 netwatch-server）
  NW_PG_SERVICE    PostgreSQL 单元名（默认 postgresql；置空=跳过）
  NW_VM_SERVICE    VictoriaMetrics 单元名（默认 victoriametrics；置空=跳过）
  NW_HEALTH_URL    健康检查地址（默认按单元 NW_LISTEN 推导 http://127.0.0.1:<port>/api/health）
  NW_START_TIMEOUT 同 --timeout
  NO_COLOR=1       关闭彩色输出

示例:
  sudo script/start.sh                  # 启动整套中心端
  sudo script/start.sh --app-only       # 仅启动 netwatch-server
  sudo script/start.sh --no-wait        # 启动后立即返回（由 systemd 守护）
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

if ! nw_start_stack "$APP_ONLY" "$WAIT"; then
  nw_err "启动未完成。排查：journalctl -u $NW_SERVICE -n 100 --no-pager"
  exit 1
fi

nw_status
nw_print_access
nw_ok "中心端启动完成"
