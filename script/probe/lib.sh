#!/usr/bin/env bash
# NetWatch 探针端控制脚本公共库（由 start.sh / stop.sh / restart.sh / status.sh source）
# 运行位置：探针主机（楼层 VM）；中心端批量调用见 batch.sh。
# 约定：调用方已设置 set -euo pipefail；本文件只定义函数，不执行动作。
# shellcheck shell=bash

: "${NW_AGENT_SERVICE:=netwatch-agent}"
: "${NW_AGENT_BIN:=/usr/local/bin/netwatch-agent}"
: "${NW_AGENT_STATE_DIR:=/var/lib/netwatch-agent}"
: "${NW_AGENT_START_TIMEOUT:=30}"
: "${NW_AGENT_STOP_TIMEOUT:=20}"
: "${NW_AGENT_LOCK_FILE:=/run/netwatch-agent-ctl.lock}"

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  AG_C_G=$'\033[32m'; AG_C_Y=$'\033[33m'; AG_C_R=$'\033[31m'; AG_C_B=$'\033[36m'; AG_C_N=$'\033[0m'
else
  AG_C_G=''; AG_C_Y=''; AG_C_R=''; AG_C_B=''; AG_C_N=''
fi

ag_info() { printf '%s[信息]%s %s\n' "$AG_C_B" "$AG_C_N" "$*"; }
ag_ok()   { printf '%s[ OK ]%s %s\n' "$AG_C_G" "$AG_C_N" "$*"; }
ag_warn() { printf '%s[警告]%s %s\n' "$AG_C_Y" "$AG_C_N" "$*"; }
ag_err()  { printf '%s[失败]%s %s\n' "$AG_C_R" "$AG_C_N" "$*" >&2; }
ag_step() { printf '\n%s===== %s =====%s\n' "$AG_C_B" "$*" "$AG_C_N"; }
ag_die()  { ag_err "$*"; exit 1; }

ag_require_root() {
  [ "$(id -u)" -eq 0 ] || ag_die "需要 root 权限（systemctl 管理探针服务）：请使用 sudo $0"
}

ag_require_cmd() {
  command -v "$1" >/dev/null 2>&1 || ag_die "缺少命令：$1"
}

ag_validate_config() {
  case "${NW_AGENT_START_TIMEOUT}" in ''|*[!0-9]*) ag_die "NW_AGENT_START_TIMEOUT 必须是整数秒";; esac
  case "${NW_AGENT_STOP_TIMEOUT}"  in ''|*[!0-9]*) ag_die "NW_AGENT_STOP_TIMEOUT 必须是整数秒";; esac
}

ag_lock() {
  command -v flock >/dev/null 2>&1 || { ag_warn "未安装 flock，跳过并发保护"; return 0; }
  if ! ( : >>"$NW_AGENT_LOCK_FILE" ) 2>/dev/null; then
    ag_warn "无法创建锁文件 $NW_AGENT_LOCK_FILE，跳过并发保护"
    return 0
  fi
  exec 9>"$NW_AGENT_LOCK_FILE"
  flock -n 9 || ag_die "另一个探针控制脚本正在运行（锁：$NW_AGENT_LOCK_FILE）"
}

# ---------- 查询 ----------
ag_unit_exists() {
  [ "$(systemctl show "$NW_AGENT_SERVICE" -p LoadState --value 2>/dev/null || true)" = "loaded" ]
}

ag_active() {
  systemctl is-active --quiet "$NW_AGENT_SERVICE" 2>/dev/null
}

ag_enabled() {
  case "$(systemctl is-enabled "$NW_AGENT_SERVICE" 2>/dev/null || true)" in
    enabled|enabled-runtime) return 0 ;;
    *) return 1 ;;
  esac
}

ag_version() {
  if [ -x "$NW_AGENT_BIN" ]; then
    "$NW_AGENT_BIN" -version 2>&1 | sed -n 's/.*netwatch-agent[[:space:]]*//p' | head -n 1
  fi
}

ag_identity_id() {
  local f="$NW_AGENT_STATE_DIR/identity.json"
  [ -f "$f" ] || return 0
  sed -n 's/.*"agent_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$f" 2>/dev/null | head -n 1
}

ag_center_url() {
  local unit="/etc/systemd/system/$NW_AGENT_SERVICE.service"
  [ -f "$unit" ] || return 0
  sed -n 's/^ExecStart=.*-server[= ]\+\([^ ]*\).*/\1/p' "$unit" 2>/dev/null | head -n 1
}

ag_tail() {
  local lines="${1:-15}"
  printf '%s\n' "----- journalctl -u $NW_AGENT_SERVICE（最近 $lines 行）-----" >&2
  journalctl -u "$NW_AGENT_SERVICE" -n "$lines" --no-pager 2>/dev/null >&2 || true
  printf '%s\n' "----------------------------------------" >&2
}

ag_wait_active() {
  local deadline=$((SECONDS + NW_AGENT_START_TIMEOUT))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if ag_active; then
      ag_ok "服务已进入 active"
      return 0
    fi
    sleep 1
  done
  ag_err "服务在 ${NW_AGENT_START_TIMEOUT} 秒内未进入 active"
  return 1
}

# ---------- 动作 ----------
ag_ensure_enabled() {
  if ag_enabled; then
    return 0
  fi
  if systemctl enable "$NW_AGENT_SERVICE" >/dev/null 2>&1; then
    ag_ok "已设置开机自启（systemctl enable $NW_AGENT_SERVICE）"
  else
    ag_warn "开机自启设置失败（可手动执行：systemctl enable $NW_AGENT_SERVICE）"
  fi
}

# $1=wait(0/1)
ag_start() {
  local wait="${1:-1}" pid
  if ! ag_unit_exists; then
    ag_err "未找到 systemd 单元 $NW_AGENT_SERVICE（请先执行 install-agent.sh）"
    return 1
  fi
  ag_ensure_enabled
  if ag_active; then
    pid=$(systemctl show "$NW_AGENT_SERVICE" -p MainPID --value 2>/dev/null || true)
    ag_ok "探针服务已在运行（PID ${pid:-未知}，版本 $(ag_version || echo 未知)）"
    return 0
  fi
  ag_info "启动探针服务（$NW_AGENT_SERVICE）..."
  if ! systemctl start "$NW_AGENT_SERVICE"; then
    ag_err "探针服务启动失败"
    ag_tail
    return 1
  fi
  if [ "$wait" -eq 1 ]; then
    if ! ag_wait_active; then
      ag_tail
      return 1
    fi
  fi
  pid=$(systemctl show "$NW_AGENT_SERVICE" -p MainPID --value 2>/dev/null || true)
  ag_ok "探针服务已启动（PID ${pid:-未知}，版本 $(ag_version || echo 未知)）"
  if [ ! -f "$NW_AGENT_STATE_DIR/identity.json" ]; then
    ag_warn "尚未生成身份文件，首次注册可能需要约 30 秒；查看：journalctl -u $NW_AGENT_SERVICE -f"
  fi
}

ag_stop() {
  if ! ag_unit_exists; then
    ag_err "未找到 systemd 单元 $NW_AGENT_SERVICE"
    return 1
  fi
  if ! ag_active; then
    ag_ok "探针服务已处于停止状态"
    return 0
  fi
  ag_info "停止探针服务（$NW_AGENT_SERVICE）..."
  if ! systemctl stop "$NW_AGENT_SERVICE"; then
    ag_err "探针服务停止失败"
    ag_tail
    return 1
  fi
  local deadline=$((SECONDS + NW_AGENT_STOP_TIMEOUT))
  while ag_active && [ "$SECONDS" -lt "$deadline" ]; do
    sleep 1
  done
  if ag_active; then
    ag_err "探针服务在 ${NW_AGENT_STOP_TIMEOUT} 秒内未停止"
    ag_tail
    return 1
  fi
  ag_ok "探针服务已停止（本地缓存与身份保留在 $NW_AGENT_STATE_DIR）"
}

# $1=wait(0/1)
ag_restart() {
  local wait="${1:-1}"
  if ! ag_stop; then
    return 1
  fi
  if ! ag_start "$wait"; then
    return 1
  fi
}

ag_status() {
  local active enabled pid since ver id center
  ag_step "NetWatch 探针状态"
  if ! ag_unit_exists; then
    ag_err "未找到 systemd 单元 $NW_AGENT_SERVICE"
    return 1
  fi
  active=$(systemctl is-active "$NW_AGENT_SERVICE" 2>/dev/null || true)
  enabled=$(systemctl is-enabled "$NW_AGENT_SERVICE" 2>/dev/null || true)
  pid=$(systemctl show "$NW_AGENT_SERVICE" -p MainPID --value 2>/dev/null || true)
  since=$(systemctl show "$NW_AGENT_SERVICE" -p ActiveEnterTimestamp --value 2>/dev/null || true)
  ver=$(ag_version)
  id=$(ag_identity_id)
  center=$(ag_center_url)
  printf '  服务      : %s / 开机自启 %s / PID %s\n' "${active:-unknown}" "${enabled:-unknown}" "${pid:-0}"
  printf '  版本      : %s\n' "${ver:-未知}"
  printf '  身份 ID   : %s\n' "${id:-未注册/不可读}"
  printf '  中心地址  : %s\n' "${center:-未知}"
  printf '  状态目录  : %s\n' "$NW_AGENT_STATE_DIR"
  printf '  启动时间  : %s\n' "${since:-未知}"
  printf '  最近日志  :\n'
  journalctl -u "$NW_AGENT_SERVICE" -n 3 --no-pager 2>/dev/null | sed 's/^/    /' || true
  [ "$active" = "active" ]
}
