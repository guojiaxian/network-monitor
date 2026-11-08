#!/usr/bin/env bash
# NetWatch 探针端 —— 状态查看脚本（在探针主机上运行，只读）
# 展示 active/开机自启/PID/版本/身份/中心地址/最近日志。
# 退出码：0=服务 active；1=未运行或单元不存在。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=script/probe/lib.sh
. "$SCRIPT_DIR/lib.sh"

usage() {
  cat <<'EOF'
用法: status.sh [选项]

查看探针 netwatch-agent 状态（只读，无需 root）。

退出码:
  0  服务运行中
  1  服务未运行或单元不存在

环境变量:
  NW_AGENT_SERVICE / NW_AGENT_BIN / NW_AGENT_STATE_DIR / NO_COLOR
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
ag_require_cmd systemctl

if ag_status; then
  ag_ok "探针服务运行正常"
  exit 0
fi
ag_err "探针服务未运行"
exit 1
