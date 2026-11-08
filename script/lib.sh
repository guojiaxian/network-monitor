#!/usr/bin/env bash
# NetWatch 中心端控制脚本公共库（由 start.sh / stop.sh / restart.sh / status.sh source）
# 约定：调用方已设置 set -euo pipefail；本文件只定义函数，不执行任何动作。
# shellcheck shell=bash

# ---------- 可配置项（同名环境变量可覆盖） ----------
: "${NW_SERVICE:=netwatch-server}"     # 中心服务单元
: "${NW_PG_SERVICE:=postgresql}"       # PostgreSQL 单元（置空=不由本脚本管理）
: "${NW_VM_SERVICE:=victoriametrics}"  # VictoriaMetrics 单元（置空=不由本脚本管理）
: "${NW_PG_HOST:=127.0.0.1}"
: "${NW_PG_PORT:=5432}"
: "${NW_VM_URL:=}"                     # 留空=读取 server 单元 NW_VM_URL，再回退 127.0.0.1:8428
: "${NW_HEALTH_URL:=}"                 # 留空=按 server 单元 NW_LISTEN 推导 /api/health
: "${NW_START_TIMEOUT:=60}"            # 每个依赖/健康检查的等待秒数
: "${NW_STOP_TIMEOUT:=30}"             # 停止后等待进入 inactive 的秒数
: "${NW_LOCK_FILE:=/run/netwatch-ctl.lock}"

# ---------- 输出 ----------
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  NW_C_G=$'\033[32m'; NW_C_Y=$'\033[33m'; NW_C_R=$'\033[31m'; NW_C_B=$'\033[36m'; NW_C_N=$'\033[0m'
else
  NW_C_G=''; NW_C_Y=''; NW_C_R=''; NW_C_B=''; NW_C_N=''
fi

nw_info() { printf '%s[信息]%s %s\n' "$NW_C_B" "$NW_C_N" "$*"; }
nw_ok()   { printf '%s[ OK ]%s %s\n' "$NW_C_G" "$NW_C_N" "$*"; }
nw_warn() { printf '%s[警告]%s %s\n' "$NW_C_Y" "$NW_C_N" "$*"; }
nw_err()  { printf '%s[失败]%s %s\n' "$NW_C_R" "$NW_C_N" "$*" >&2; }
nw_step() { printf '\n%s===== %s =====%s\n' "$NW_C_B" "$*" "$NW_C_N"; }
nw_die()  { nw_err "$*"; exit 1; }

# ---------- 前置检查 ----------
nw_require_root() {
  [ "$(id -u)" -eq 0 ] || nw_die "需要 root 权限（systemctl 管理服务）：请使用 sudo $0"
}

nw_require_cmd() {
  command -v "$1" >/dev/null 2>&1 || nw_die "缺少命令：$1"
}

nw_validate_config() {
  case "${NW_START_TIMEOUT}" in ''|*[!0-9]*) nw_die "NW_START_TIMEOUT 必须是整数秒，当前：${NW_START_TIMEOUT}";; esac
  case "${NW_STOP_TIMEOUT}"  in ''|*[!0-9]*) nw_die "NW_STOP_TIMEOUT 必须是整数秒，当前：${NW_STOP_TIMEOUT}";; esac
}

# 并发保护：同一时刻只允许一个控制脚本执行（避免 start/stop/restart 交叉）
nw_lock() {
  command -v flock >/dev/null 2>&1 || { nw_warn "未安装 flock，跳过并发保护"; return 0; }
  if ! ( : >>"$NW_LOCK_FILE" ) 2>/dev/null; then
    nw_warn "无法创建锁文件 $NW_LOCK_FILE，跳过并发保护"
    return 0
  fi
  exec 9>"$NW_LOCK_FILE"
  flock -n 9 || nw_die "另一个 NetWatch 控制脚本正在运行（锁：$NW_LOCK_FILE），请稍后重试"
}

# ---------- 单元查询 ----------
nw_unit_exists() {
  [ "$(systemctl show "$1" -p LoadState --value 2>/dev/null || true)" = "loaded" ]
}

nw_is_active() {
  systemctl is-active --quiet "$1" 2>/dev/null
}

nw_is_enabled() {
  case "$(systemctl is-enabled "$1" 2>/dev/null || true)" in
    enabled|enabled-runtime) return 0 ;;
    *) return 1 ;;
  esac
}

# 读取 server 单元 Environment= 中的变量（仅用于取 NW_LISTEN / NW_VM_URL）
nw_unit_env() {
  local key="$1" raw
  raw=$(systemctl show "$NW_SERVICE" -p Environment --value 2>/dev/null || true)
  [ -n "$raw" ] || return 0
  printf '%s\n' "$raw" | tr ' ' '\n' | sed -n "s/^${key}=//p" | head -n 1
}

nw_vm_url() {
  if [ -n "${NW_VM_URL:-}" ]; then
    printf '%s' "$NW_VM_URL"
    return 0
  fi
  local url
  url=$(nw_unit_env NW_VM_URL)
  printf '%s' "${url:-http://127.0.0.1:8428}"
}

nw_health_url() {
  if [ -n "${NW_HEALTH_URL:-}" ]; then
    printf '%s' "$NW_HEALTH_URL"
    return 0
  fi
  local listen port
  listen=$(nw_unit_env NW_LISTEN)
  port=${listen##*:}
  case "$port" in ''|*[!0-9]*) port=8080 ;; esac
  printf 'http://127.0.0.1:%s/api/health' "$port"
}

# ---------- 就绪探测 ----------
nw_pg_ready() {
  if command -v pg_isready >/dev/null 2>&1; then
    pg_isready -q -h "$NW_PG_HOST" -p "$NW_PG_PORT" >/dev/null 2>&1
  else
    (exec 3<>"/dev/tcp/${NW_PG_HOST}/${NW_PG_PORT}") >/dev/null 2>&1
  fi
}

nw_vm_ready() {
  curl -fsS -m 3 "$(nw_vm_url)/health" >/dev/null 2>&1
}

nw_health_json() {
  curl -fsS -m 3 "$(nw_health_url)" 2>/dev/null || true
}

# 中心服务健康 = /api/health 返回 status=ok（degraded 视为未就绪）
nw_server_ready() {
  local json status
  json=$(curl -fsS -m 3 "$(nw_health_url)" 2>/dev/null) || return 1
  status=$(printf '%s' "$json" | sed -n 's/.*"status"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
  [ "$status" = "ok" ]
}

nw_wait_until() {
  local timeout="$1" label="$2"
  shift 2
  local deadline=$((SECONDS + timeout))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if "$@"; then
      nw_ok "$label 就绪"
      return 0
    fi
    sleep 1
  done
  nw_err "$label 在 ${timeout} 秒内未就绪"
  return 1
}

nw_tail() {
  local unit="$1" lines="${2:-15}"
  printf '%s\n' "----- journalctl -u $unit（最近 $lines 行）-----" >&2
  journalctl -u "$unit" -n "$lines" --no-pager 2>/dev/null >&2 || true
  printf '%s\n' "----------------------------------------" >&2
}

# ---------- 单元操作 ----------
nw_ensure_enabled() {
  local unit="$1" state
  state=$(systemctl is-enabled "$unit" 2>/dev/null || true)
  case "$state" in
    enabled|enabled-runtime) return 0 ;;
    static|indirect|generated|transient) return 0 ;;  # 本身无需/无法 enable
  esac
  if systemctl enable "$unit" >/dev/null 2>&1; then
    nw_ok "$unit 已设置开机自启"
  else
    nw_warn "$unit 开机自启设置失败（可手动执行：systemctl enable $unit）"
  fi
}

nw_start_unit() {
  local unit="$1" label="$2" pid
  if ! nw_unit_exists "$unit"; then
    nw_err "未找到 systemd 单元 $unit"
    return 1
  fi
  nw_ensure_enabled "$unit"
  if nw_is_active "$unit"; then
    pid=$(systemctl show "$unit" -p MainPID --value 2>/dev/null || true)
    nw_ok "$label（$unit）已在运行（PID ${pid:-未知}）"
    return 0
  fi
  nw_info "启动 $label（$unit）..."
  if ! systemctl start "$unit"; then
    nw_err "$label 启动失败"
    nw_tail "$unit"
    return 1
  fi
  if ! nw_is_active "$unit"; then
    nw_err "$label 启动后未进入 active 状态"
    nw_tail "$unit"
    return 1
  fi
  pid=$(systemctl show "$unit" -p MainPID --value 2>/dev/null || true)
  nw_ok "$label 已启动（PID ${pid:-未知}）"
}

nw_stop_unit() {
  local unit="$1" label="$2"
  if ! nw_unit_exists "$unit"; then
    nw_err "未找到 systemd 单元 $unit"
    return 1
  fi
  if ! nw_is_active "$unit"; then
    nw_ok "$label（$unit）已处于停止状态"
    return 0
  fi
  nw_info "停止 $label（$unit）..."
  if ! systemctl stop "$unit"; then
    nw_err "$label 停止失败"
    nw_tail "$unit"
    return 1
  fi
  local deadline=$((SECONDS + NW_STOP_TIMEOUT))
  while nw_is_active "$unit" && [ "$SECONDS" -lt "$deadline" ]; do
    sleep 1
  done
  if nw_is_active "$unit"; then
    nw_err "$label 在 ${NW_STOP_TIMEOUT} 秒内未停止"
    nw_tail "$unit"
    return 1
  fi
  nw_ok "$label 已停止"
}

# ---------- 组合动作 ----------
# $1=app_only(0/1) $2=wait(0/1)
nw_start_stack() {
  local app_only="${1:-0}" wait="${2:-1}"

  if [ "$app_only" -eq 0 ]; then
    nw_step "启动依赖服务"
    if [ -n "${NW_PG_SERVICE:-}" ]; then
      if ! nw_start_unit "$NW_PG_SERVICE" "PostgreSQL"; then return 1; fi
      if [ "$wait" -eq 1 ]; then
        if ! nw_wait_until "$NW_START_TIMEOUT" "PostgreSQL 接受连接" nw_pg_ready; then
          nw_tail "$NW_PG_SERVICE"
          return 1
        fi
      fi
    else
      nw_info "未设置 NW_PG_SERVICE，跳过 PostgreSQL"
    fi
    if [ -n "${NW_VM_SERVICE:-}" ]; then
      if ! nw_start_unit "$NW_VM_SERVICE" "VictoriaMetrics"; then return 1; fi
      if [ "$wait" -eq 1 ]; then
        if ! nw_wait_until "$NW_START_TIMEOUT" "VictoriaMetrics 健康检查" nw_vm_ready; then
          nw_tail "$NW_VM_SERVICE"
          return 1
        fi
      fi
    else
      nw_info "未设置 NW_VM_SERVICE，跳过 VictoriaMetrics"
    fi
  else
    nw_info "仅操作 NetWatch 中心服务（--app-only）"
  fi

  nw_step "启动 NetWatch 中心服务"
  if ! nw_start_unit "$NW_SERVICE" "NetWatch 中心服务"; then return 1; fi
  if [ "$wait" -eq 1 ]; then
    if ! nw_wait_until "$NW_START_TIMEOUT" "中心服务健康检查（$(nw_health_url)）" nw_server_ready; then
      nw_tail "$NW_SERVICE" 20
      return 1
    fi
  fi
  return 0
}

# $1=app_only(0/1)：按“中心服务 -> 时序库 -> 数据库”的逆序停止
nw_stop_stack() {
  local app_only="${1:-0}"

  nw_step "停止 NetWatch 中心服务"
  if ! nw_stop_unit "$NW_SERVICE" "NetWatch 中心服务"; then return 1; fi
  if [ "$app_only" -eq 1 ]; then
    nw_info "仅停止中心服务，依赖服务保持运行（--app-only）"
    return 0
  fi
  if [ -n "${NW_VM_SERVICE:-}" ]; then
    if ! nw_stop_unit "$NW_VM_SERVICE" "VictoriaMetrics"; then return 1; fi
  fi
  if [ -n "${NW_PG_SERVICE:-}" ]; then
    if ! nw_stop_unit "$NW_PG_SERVICE" "PostgreSQL"; then return 1; fi
  fi
  return 0
}

# ---------- 状态展示 ----------
nw_print_unit() {
  local unit="$1" label="$2" active enabled pid
  if ! nw_unit_exists "$unit"; then
    printf '  %-20s: 单元不存在\n' "$label"
    return 0
  fi
  active=$(systemctl is-active "$unit" 2>/dev/null || true)
  enabled=$(systemctl is-enabled "$unit" 2>/dev/null || true)
  pid=$(systemctl show "$unit" -p MainPID --value 2>/dev/null || true)
  printf '  %s: %s / 开机自启 %s / PID %s\n' "$label" "${active:-unknown}" "${enabled:-unknown}" "${pid:-0}"
}

nw_status() {
  local json url
  nw_step "NetWatch 中心端状态"
  nw_print_unit "$NW_PG_SERVICE" "PostgreSQL"
  nw_print_unit "$NW_VM_SERVICE" "VictoriaMetrics"
  nw_print_unit "$NW_SERVICE" "NetWatch 中心"
  url=$(nw_health_url)
  json=$(nw_health_json)
  if [ -n "$json" ]; then
    printf '  健康检查（%s）: %s\n' "$url" "$json"
  else
    printf '  健康检查（%s）: 不可达\n' "$url"
  fi
}

nw_print_access() {
  local listen port ip
  listen=$(nw_unit_env NW_LISTEN)
  port=${listen##*:}
  case "$port" in ''|*[!0-9]*) port=8080 ;; esac
  ip=$(hostname -I 2>/dev/null | awk '{print $1}')
  [ -n "$ip" ] || ip="127.0.0.1"
  if [ "$port" = "80" ]; then
    printf '  管理后台: http://%s/\n' "$ip"
    printf '  监控大屏: http://%s/#/screen\n' "$ip"
  else
    printf '  管理后台: http://%s:%s/\n' "$ip" "$port"
    printf '  监控大屏: http://%s:%s/#/screen\n' "$ip" "$port"
  fi
}
