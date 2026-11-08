#!/usr/bin/env bash
# NetWatch 中心端 —— 探针批量运维脚本（在中心 10.8.14.200 上运行）
# 通过 SSH 免密批量操作探针：下发控制脚本、设置开机自启、启停/重启/查看状态。
# 默认探针范围 10.8.14.201-212，可用 NW_PROBES 或 --hosts 覆盖。
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

DEFAULT_HOSTS="10.8.14.201 10.8.14.202 10.8.14.203 10.8.14.204 10.8.14.205 10.8.14.206 10.8.14.207 10.8.14.208 10.8.14.209 10.8.14.210 10.8.14.211 10.8.14.212"
HOSTS="${NW_PROBES:-$DEFAULT_HOSTS}"
REMOTE_DIR="${NW_REMOTE_DIR:-/usr/local/sbin/netwatch-probe}"
SSH_USER="${NW_SSH_USER:-root}"
SSH_OPTS=(-o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=5 -o LogLevel=ERROR)
REMOTE_FILES=(lib.sh start.sh stop.sh restart.sh status.sh)

C_G=$'\033[32m'; C_R=$'\033[31m'; C_Y=$'\033[33m'; C_B=$'\033[36m'; C_N=$'\033[0m'
if [ ! -t 1 ] || [ -n "${NO_COLOR:-}" ]; then C_G=''; C_R=''; C_Y=''; C_B=''; C_N=''; fi
ok()   { printf '%s[ OK ]%s %s\n' "$C_G" "$C_N" "$*"; }
err()  { printf '%s[失败]%s %s\n' "$C_R" "$C_N" "$*" >&2; }
warn() { printf '%s[警告]%s %s\n' "$C_Y" "$C_N" "$*"; }
info() { printf '%s[信息]%s %s\n' "$C_B" "$C_N" "$*"; }

usage() {
  cat <<'EOF'
用法: batch.sh <动作> [选项]

在中心端通过 SSH 免密批量操作探针（默认 10.8.14.201-212）：

动作:
  deploy   下发 start/stop/restart/status/lib.sh 到探针
           的 /usr/local/sbin/netwatch-probe/，并启用开机自启、启动服务；
           同时在 /root/netwatch-probe 建立指向该目录的软链接
  enable   仅在探针上 systemctl enable + start netwatch-agent
  start    批量执行探针端 start.sh
  stop     批量执行探针端 stop.sh
  restart  批量执行探针端 restart.sh
  status   批量执行探针端 status.sh（详细状态）
  check    汇总输出 主机名|active|enabled|版本（默认动作）

选项:
  --hosts "IP..."   指定探针 IP（空格分隔），也可用 NW_PROBES 环境变量
  -h, --help        显示本帮助

环境变量:
  NW_PROBES      探针 IP 列表（默认 10.8.14.201 ... 10.8.14.212）
  NW_SSH_USER    SSH 用户（默认 root；需已配置免密）
  NW_REMOTE_DIR  探针端脚本目录（默认 /usr/local/sbin/netwatch-probe）
  NO_COLOR=1     关闭彩色输出

示例:
  sudo script/probe/batch.sh deploy          # 首次下发脚本 + 设置自启 + 启动
  sudo script/probe/batch.sh check           # 汇总 12 台状态
  sudo script/probe/batch.sh restart         # 批量重启探针服务
EOF
}

ACTION="${1:-check}"
if [ $# -gt 0 ]; then shift; fi
while [ $# -gt 0 ]; do
  case "$1" in
    --hosts)
      [ $# -ge 2 ] || { err "--hosts 需要参数"; exit 2; }
      HOSTS="$2"; shift
      ;;
    -h|--help) usage; exit 0 ;;
    *) err "未知参数：$1"; usage; exit 2 ;;
  esac
  shift
done

case "$ACTION" in
  deploy|enable|start|stop|restart|status|check) ;;
  -h|--help) usage; exit 0 ;;
  *) err "未知动作：$ACTION"; usage; exit 2 ;;
esac

command -v ssh >/dev/null 2>&1 || { err "缺少命令：ssh"; exit 1; }

if [ "$ACTION" = "deploy" ]; then
  command -v scp >/dev/null 2>&1 || { err "缺少命令：scp"; exit 1; }
  for f in "${REMOTE_FILES[@]}"; do
    [ -f "$SCRIPT_DIR/$f" ] || { err "缺少脚本：$SCRIPT_DIR/$f"; exit 1; }
  done
fi

# 并发保护（中心端；非 root 时跳过）
if command -v flock >/dev/null 2>&1 && ( : >>/run/netwatch-probe-batch.lock ) 2>/dev/null; then
  exec 9>/run/netwatch-probe-batch.lock
  flock -n 9 || { err "另一个探针批量脚本正在运行"; exit 1; }
fi

printf '%s===== 探针批量操作：%s（%s）=====%s\n' "$C_B" "$ACTION" "$HOSTS" "$C_N"

ok_count=0
fail_count=0
for ip in $HOSTS; do
  case "$ip" in
    ''|*[!0-9A-Za-z.-]*) err "非法主机地址：$ip"; fail_count=$((fail_count+1)); continue ;;
  esac

  case "$ACTION" in
    deploy)
      if ! ssh "${SSH_OPTS[@]}" "$SSH_USER@$ip" "mkdir -p '$REMOTE_DIR'"; then
        err "$ip SSH 连接/建目录失败"
        fail_count=$((fail_count+1))
        continue
      fi
      if ! scp -p -q "${SSH_OPTS[@]}" \
          "$SCRIPT_DIR/lib.sh" "$SCRIPT_DIR/start.sh" "$SCRIPT_DIR/stop.sh" \
          "$SCRIPT_DIR/restart.sh" "$SCRIPT_DIR/status.sh" \
          "$SSH_USER@$ip:$REMOTE_DIR/"; then
        err "$ip 脚本下发失败"
        fail_count=$((fail_count+1))
        continue
      fi
      if out=$(ssh "${SSH_OPTS[@]}" "$SSH_USER@$ip" \
          "chmod 0755 '$REMOTE_DIR'/*.sh && '$REMOTE_DIR/start.sh' --no-wait && { if [ -e /root/netwatch-probe ] && [ ! -L /root/netwatch-probe ]; then echo LINK_SKIPPED; else ln -sfn '$REMOTE_DIR' /root/netwatch-probe && echo LINK_OK; fi; }" 2>&1); then
        case "$out" in
          *LINK_OK*)
            ok "$ip 脚本已下发到 $REMOTE_DIR，已设置开机自启并启动（快捷入口 /root/netwatch-probe）"
            ;;
          *LINK_SKIPPED*)
            warn "$ip 脚本已下发并启动，但 /root/netwatch-probe 已存在且不是软链接，未覆盖"
            ;;
          *)
            ok "$ip 脚本已下发到 $REMOTE_DIR，已设置开机自启并启动"
            ;;
        esac
        ok_count=$((ok_count+1))
      else
        err "$ip 启用/启动失败"
        printf '%s\n' "$out" | sed 's/^/    /' >&2
        fail_count=$((fail_count+1))
      fi
      ;;
    enable)
      if out=$(ssh "${SSH_OPTS[@]}" "$SSH_USER@$ip" \
          "'$REMOTE_DIR/start.sh' --no-wait && systemctl is-enabled netwatch-agent && systemctl is-active netwatch-agent" 2>&1); then
        ok "$ip 已设置开机自启并启动（$(printf '%s' "$out" | tail -n 2 | tr '\n' '/' )）"
        ok_count=$((ok_count+1))
      else
        err "$ip 设置自启/启动失败（先执行 deploy？）"
        printf '%s\n' "$out" | sed 's/^/    /' >&2
        fail_count=$((fail_count+1))
      fi
      ;;
    start|stop|restart|status)
      if out=$(ssh "${SSH_OPTS[@]}" "$SSH_USER@$ip" "'$REMOTE_DIR/$ACTION.sh'" 2>&1); then
        printf '%s\n' "$out" | sed "s/^/[$ip] /"
        ok_count=$((ok_count+1))
      else
        err "$ip 执行 $ACTION 失败"
        printf '%s\n' "$out" | sed 's/^/    /' >&2
        fail_count=$((fail_count+1))
      fi
      ;;
    check)
      if out=$(ssh "${SSH_OPTS[@]}" "$SSH_USER@$ip" \
          'printf "%s|%s|%s|%s|%s\n" "$(hostname)" "$(systemctl is-active netwatch-agent 2>/dev/null)" "$(systemctl is-enabled netwatch-agent 2>/dev/null)" "$(/usr/local/bin/netwatch-agent -version 2>&1 | sed -n "s/.*netwatch-agent //p")" "$(stat -c %s /usr/local/bin/netwatch-agent 2>/dev/null)"' 2>&1); then
        printf '  %-14s %s\n' "$ip" "$out"
        ok_count=$((ok_count+1))
      else
        err "$ip 状态查询失败"
        printf '%s\n' "$out" | sed 's/^/    /' >&2
        fail_count=$((fail_count+1))
      fi
      ;;
  esac
done

printf '\n成功 %d 台，失败 %d 台\n' "$ok_count" "$fail_count"
[ "$fail_count" -eq 0 ]
