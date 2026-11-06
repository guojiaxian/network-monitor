#!/usr/bin/env bash
# NetWatch 探针一键安装（全新银河麒麟 V10 探针 VM，root 运行）
#
# 自动完成：环境检查 → 与中心对时（漂移自动配 chrony）→ 中心连通性检查
#          → 安装二进制与 systemd 服务 → 启动并确认注册成功。
# 二进制查找顺序：参数3 > 脚本同目录 > 包内 bin/ > 中心端下载。
#
# 用法：
#   ./install-agent.sh <中心地址> <注册令牌> [探针二进制]
# 示例：
#   ./install-agent.sh http://10.8.0.10:8080 s3cret-token
set -euo pipefail

SERVER="${1:-}"
TOKEN="${2:-}"
BIN="${3:-}"
NAME="${4:-${NW_PROBE_NAME:-}}"   # 探针名称（如：探针1），将同时设置 OS 主机名（仅 ASCII 时）

C_G="\033[32m"; C_R="\033[31m"; C_Y="\033[33m"; C_N="\033[0m"
ok()   { echo -e "${C_G}[OK]${C_N} $*"; }
warn() { echo -e "${C_Y}[警告]${C_N} $*"; }
err()  { echo -e "${C_R}[失败]${C_N} $*"; }
step() { echo -e "\n========== $* =========="; }

trap 'err "安装在第 $LINENO 行失败，请根据输出排查后重跑"' ERR

# ---------- 1. 基础检查 ----------
step "1/6 基础环境检查"
[[ -n "$SERVER" && -n "$TOKEN" ]] || { echo "用法: $0 <中心地址> <注册令牌> [探针二进制] [探针名称]"; exit 1; }
[[ $EUID -eq 0 ]] || { err "请以 root 运行"; exit 1; }
ok "root 权限"

# ---------- 中心公钥（可选）：NW_SSH_PUBKEY 环境变量携带时安装，
# 实现中心服务器→本机免密 SSH（适合探针控制台直装、中心尚未能 SSH 到达时） ----------
if [ -n "${NW_SSH_PUBKEY:-}" ]; then
  mkdir -p ~/.ssh && chmod 700 ~/.ssh
  touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys
  if grep -qxF "$NW_SSH_PUBKEY" ~/.ssh/authorized_keys; then
    ok "中心公钥已存在（免密 SSH 就绪）"
  else
    echo "$NW_SSH_PUBKEY" >> ~/.ssh/authorized_keys
    ok "已安装中心公钥，中心服务器可免密 SSH 本机"
  fi
fi

# ---------- 探针名称：设置 OS 主机名（克隆模板必做） ----------
# 中文等非 ASCII 名称转安全形式时会被替换成 -（如"探针-201"→"201"），
# 纯数字主机名不合规也不易辨识：开头是数字时自动加 "probe-" 前缀（→"probe-201"）。
if [ -n "$NAME" ] && command -v hostnamectl >/dev/null; then
  CUR_HOST=$(hostname)
  SAFE=$(echo "$NAME" | LC_ALL=C sed 's/[^a-zA-Z0-9.-]/-/g; s/-\+/-/g; s/^-//; s/-$//')
  case "$SAFE" in [0-9]*) SAFE="probe-$SAFE" ;; esac
  if [ -n "$SAFE" ] && [ "$CUR_HOST" != "$SAFE" ]; then
    hostnamectl set-hostname "$SAFE" && ok "OS 主机名已设置为: $SAFE"
    [ "$SAFE" != "$NAME" ] && warn "名称含非 ASCII 字符，OS 主机名使用 ASCII 形式 $SAFE（NetWatch 内显示名称仍为 $NAME）"
  fi
fi

ARCH=$(uname -m)
case "$ARCH" in
  x86_64) A=amd64 ;;
  aarch64) A=arm64 ;;
  *) err "不支持的架构: $ARCH"; exit 1 ;;
esac
ok "架构 $ARCH"

command -v systemctl >/dev/null || { err "未检测到 systemd"; exit 1; }
ok "systemd 可用"

# ---------- 2. 中心连通性 ----------
step "2/6 中心端连通性"
if curl -fsS -m 8 "$SERVER/healthz" >/dev/null 2>&1; then
  ok "中心端可达: $SERVER"
else
  err "无法访问中心端 $SERVER/healthz（检查地址、网络、防火墙）"; exit 1
fi

# ---------- 3. 时间同步（漂移>30s 自动配 chrony） ----------
step "3/6 时间同步"
CENTER_DATE=$(curl -fsS -D - -o /dev/null -m 8 "$SERVER/healthz" 2>/dev/null | grep -i '^date:' | cut -d' ' -f2- | tr -d '\r')
CENTER_EPOCH=0
if [ -n "$CENTER_DATE" ]; then
  CENTER_EPOCH=$(date -u -d "$CENTER_DATE" +%s 2>/dev/null || echo 0)
fi
if [ "$CENTER_EPOCH" = "0" ]; then
  warn "无法从中心端响应解析时间，跳过对时检查（请自行确认已配 NTP）"
else
  LOCAL_EPOCH=$(date +%s)
  DRIFT=$((LOCAL_EPOCH - CENTER_EPOCH))
  ADRIFT=${DRIFT#-}
  ok "与中心时钟偏差 ${DRIFT}s"
  if [ "$ADRIFT" -gt 30 ]; then
    warn "时钟偏差超 30s，自动配置 chrony..."
    PKG="yum"; command -v dnf >/dev/null && PKG="dnf"
    rpm -q chrony >/dev/null 2>&1 || $PKG install -y chrony
    systemctl enable --now chronyd
    sleep 6
    LOCAL_EPOCH=$(date +%s)
    DRIFT=$((LOCAL_EPOCH - CENTER_EPOCH)); ADRIFT=${DRIFT#-}
    if [ "$ADRIFT" -gt 30 ]; then
      warn "偏差仍为 ${DRIFT}s——样本时间可能失真，请人工校时（chronyc sources / 手动 date -s）"
    else
      ok "对时完成"
    fi
  fi
fi

# ---------- 4. 安装二进制与服务 ----------
step "4/6 安装探针"
if [[ -z "$BIN" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
  for cand in "$SCRIPT_DIR/netwatch-agent" "$SCRIPT_DIR/../bin/netwatch-agent"; do
    [ -f "$cand" ] && { BIN="$cand"; break; }
  done
fi
[[ -n "$BIN" && -f "$BIN" ]] || { err "未找到探针二进制：请传入第 3 个参数或将 netwatch-agent 与本脚本放同目录"; exit 1; }

install -m 0755 "$BIN" /usr/local/bin/netwatch-agent
mkdir -p /var/lib/netwatch-agent
ok "二进制安装至 /usr/local/bin/netwatch-agent"

cat > /etc/systemd/system/netwatch-agent.service <<UNIT
[Unit]
Description=NetWatch network probe agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/netwatch-agent -server ${SERVER} -token ${TOKEN} -name "${NAME}"
Restart=always
RestartSec=5
AmbientCapabilities=CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_RAW
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/netwatch-agent /usr/local/bin
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now netwatch-agent
ok "服务已启动"

# ---------- 5. 注册确认 ----------
step "5/6 注册确认（等待 30s）"
REGISTERED=""
for i in $(seq 1 6); do
  sleep 5
  if journalctl -u netwatch-agent --since "2 minutes ago" 2>/dev/null | grep -q "registered as agent"; then
    REGISTERED=$(journalctl -u netwatch-agent --since "2 minutes ago" | grep -o "registered as agent [0-9a-f]*" | tail -1 | awk '{print $NF}')
    break
  fi
done
if [ -z "$REGISTERED" ] && [ -f /var/lib/netwatch-agent/identity.json ] && systemctl is-active --quiet netwatch-agent; then
  REGISTERED="已有身份（复用）: $(python3 -c "import json;print(json.load(open('/var/lib/netwatch-agent/identity.json'))['agent_id'])" 2>/dev/null || echo 'identity.json 存在')"
fi
if [ -n "$REGISTERED" ]; then
  ok "已注册：${REGISTERED:0:24}"
else
  warn "未确认注册（可能令牌错误或仍在上报）——检查: journalctl -u netwatch-agent"
fi

# ---------- 6. 完成 ----------
step "6/6 完成"
systemctl --no-pager --lines 5 status netwatch-agent | head -8 || true
echo -e "${C_G}探针安装完成。${C_N}最后一步：到管理后台「探针管理」将本探针绑定到对应线路（先绑定再正式启用）。"
