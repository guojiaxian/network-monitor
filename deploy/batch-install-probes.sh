#!/usr/bin/env bash
# 探针批量安装：按 CSV 清单逐台分发并安装（含探针名称，注册后后台直接显示）。
#
# probes.csv 每行：IP,SSH用户,名称[,端口]（默认 22，# 开头为注释）
# 用法：
#   ./batch-install-probes.sh probes.csv <中心地址> <注册令牌> [探针二进制]
#   NW_PROBE_PASS='统一密码' ./batch-install-probes.sh ...   # 可选：免密配置用（非交互）
# 示例：
#   ./batch-install-probes.sh probes.csv http://10.8.0.10:8080 s3cret-token ../bin/netwatch-agent
#
# 免密 SSH（自动完成，无需手工前置）：中心无 SSH 密钥时自动生成；对尚未
# 免密的主机自动 ssh-copy-id 下发公钥——统一密码时交互只输入一次（或用
# NW_PROBE_PASS 提供），某台密码不同会在该台失败后重新提示。
set -euo pipefail

LIST="${1:-}"
SERVER="${2:-}"
TOKEN="${3:-}"
BIN="${4:-}"

[[ -n "$LIST" && -n "$SERVER" && -n "$TOKEN" ]] || { echo "用法: $0 probes.csv <中心地址> <令牌> [二进制]"; exit 1; }
[[ -f "$LIST" ]] || { echo "清单不存在: $LIST"; exit 1; }
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$(dirname "$SCRIPT_DIR")/bin"

# ---------- 中心 SSH 密钥：无则自动生成（中心→探针免密的根基） ----------
mkdir -p ~/.ssh && chmod 700 ~/.ssh
PUB=""
for k in id_ed25519 id_ecdsa id_rsa; do
  [ -f ~/.ssh/$k.pub ] && { PUB=~/.ssh/$k.pub; break; }
done
if [ -z "$PUB" ]; then
  ssh-keygen -t ed25519 -N '' -f ~/.ssh/id_ed25519 -C "netwatch-center-$(hostname -s)" >/dev/null
  PUB=~/.ssh/id_ed25519.pub
  echo "已生成中心 SSH 密钥: $PUB"
fi

can_ssh() { ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 -p "$1" "$2" 'true' 2>/dev/null; }

PASS="${NW_PROBE_PASS:-}"

FAIL=0
while IFS=, read -r IP USER NAME PORT; do
  [[ -z "$IP" || "$IP" =~ ^# ]] && continue
  USER=${USER:-root}; PORT=${PORT:-22}; NAME=${NAME:-}
  SSH_OPTS=(-o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 -p "$PORT")
  echo "==> $IP（${NAME:-未命名}）"

  # ---- 第 1 步：免密 SSH（未配置则自动 ssh-copy-id，密码只输一次） ----
  if can_ssh "$PORT" "$USER@$IP"; then
    echo "    免密 SSH 已就绪"
  else
    if [ -z "$PASS" ]; then
      # 从 /dev/tty 读密码：while read 消耗的是清单文件，交互输入必须走 tty
      read -rs -p "    $IP 尚未免密，输入 SSH 密码（统一密码只需输入一次；直接回车跳过该主机）: " PASS </dev/tty || PASS=""
      echo
      [ -z "$PASS" ] && { echo "    未输入密码，跳过"; FAIL=1; continue; }
    fi
    if ! command -v sshpass >/dev/null; then
      echo "    未安装 sshpass，无法自动配置免密（yum install -y sshpass 或 dnf install -y sshpass）"; FAIL=1; PASS=""; continue
    fi
    if ! sshpass -p "$PASS" ssh-copy-id -i "$PUB" -o StrictHostKeyChecking=accept-new -p "$PORT" "$USER@$IP" >/dev/null 2>&1; then
      echo "    公钥下发失败（密码错误或不可达），跳过"; FAIL=1; PASS=""; continue
    fi
    if ! can_ssh "$PORT" "$USER@$IP"; then
      echo "    公钥已下发但免密仍未生效（检查 sshd PubkeyAuthentication），跳过"; FAIL=1; PASS=""; continue
    fi
    echo "    已配置免密 SSH"
  fi

  # ---- 第 2 步：分发二进制与安装脚本并安装 ----
  if [[ -n "$BIN" && -f "$BIN" ]]; then
    scp -q -P "$PORT" "$BIN" "$SCRIPT_DIR/install-agent.sh" "$USER@$IP:/tmp/"
    ssh "${SSH_OPTS[@]}" "$USER@$IP" "bash /tmp/install-agent.sh $SERVER $TOKEN /tmp/$(basename "$BIN") '$NAME'" | tail -2
  elif [[ -f "$BIN_DIR/netwatch-agent" ]]; then
    scp -q -P "$PORT" "$BIN_DIR/netwatch-agent" "$SCRIPT_DIR/install-agent.sh" "$USER@$IP:/tmp/"
    ssh "${SSH_OPTS[@]}" "$USER@$IP" "bash /tmp/install-agent.sh $SERVER $TOKEN /tmp/netwatch-agent '$NAME'" | tail -2
  else
    scp -q -P "$PORT" "$SCRIPT_DIR/install-agent.sh" "$USER@$IP:/tmp/"
    ssh "${SSH_OPTS[@]}" "$USER@$IP" "bash /tmp/install-agent.sh $SERVER $TOKEN" | tail -2
  fi
  echo "    完成"
done < "$LIST"

[[ $FAIL -eq 0 ]] && echo "全部完成" || { echo "存在失败项，请检查上方日志"; exit 1; }
