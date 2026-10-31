#!/usr/bin/env bash
# NetWatch 中心端一键部署（全新银河麒麟 V10 服务器，原生 systemd，无需 Docker）
#
# 前提：本脚本与 bin/ web/ deploy/ docs/ 同目录（即交付包解压后执行），root 运行。
# 脚本自动完成：环境检查 → chrony 对时 → 防火墙放行 → PostgreSQL 安装初始化
#              → VictoriaMetrics 安装 → 服务安装启动 → 部署自验证。
# 数据库口令自动随机生成并写入 systemd 单元。
#
# 用法：./install-server.sh [安装根目录，默认 /opt/netwatch]
#       CHECK_ONLY=1 ./install-server.sh   # 只做环境检查，不实际安装
set -euo pipefail

ROOT="${1:-/opt/netwatch}"
SRC="$(cd "$(dirname "$0")/.." && pwd)"
CHECK_ONLY="${CHECK_ONLY:-0}"
VM_VERSION="v1.102.1"

# ---------- 输出工具 ----------
C_G="\033[32m"; C_R="\033[31m"; C_Y="\033[33m"; C_N="\033[0m"
ok()   { echo -e "${C_G}[OK]${C_N} $*"; }
warn() { echo -e "${C_Y}[警告]${C_N} $*"; }
err()  { echo -e "${C_R}[失败]${C_N} $*"; }
step() { echo -e "\n========== $* =========="; }
run()  { # CHECK_ONLY=1 时只打印不执行
  if [ "$CHECK_ONLY" = "1" ]; then echo "  (检查模式，跳过执行) $*"; else eval "$@"; fi
}

trap 'err "部署在第 $LINENO 行失败，请根据上方输出排查后重跑（脚本可重复执行）"' ERR

# ---------- 1. 基础环境检查 ----------
step "1/8 基础环境检查"
[[ $EUID -eq 0 ]] || { err "请以 root 运行"; exit 1; }
ok "root 权限"

ARCH=$(uname -m)
[[ "$ARCH" == "x86_64" ]] || { err "仅支持 x86_64，当前 $ARCH"; exit 1; }
ok "架构 x86_64"

. /etc/os-release 2>/dev/null || true
case "${ID:-}" in
  kylin|openkylin|rhel|centos|rocky|almalinux) ok "操作系统: ${PRETTY_NAME:-未知}（RHEL 系，兼容）" ;;
  *) warn "未识别的操作系统 ${ID:-未知}，将按 RHEL 系处理（yum/dnf）" ;;
esac

PKG="yum"; command -v dnf >/dev/null && PKG="dnf"
ok "包管理器: $PKG"

MEM_MB=$(free -m | awk '/^Mem:/{print $2}')
if [ "$MEM_MB" -lt 3000 ]; then warn "内存 ${MEM_MB}MB 低于建议值 4G（仍可运行，建议扩配）"; else ok "内存 ${MEM_MB}MB"; fi

DISK_G=$(df -BG /opt 2>/dev/null | awk 'NR==2{gsub("G","",$4);print $4}')
DISK_G=${DISK_G:-0}
if [ "$DISK_G" -lt 5 ]; then err "/opt 可用空间 ${DISK_G}G 不足 5G"; exit 1; fi
ok "/opt 可用空间 ${DISK_G}G"

for port in 8080 5432 8428; do
  if ss -tln 2>/dev/null | grep -q ":$port "; then
    err "端口 $port 已被占用：$(ss -tlnp | grep ":$port " | head -1)"
    exit 1
  fi
done
ok "端口 8080/5432/8428 空闲"

if [ ! -f "$SRC/bin/netwatch-server" ] || [ ! -f "$SRC/bin/victoria-metrics" ]; then
  err "缺少二进制：请解压完整交付包后在包目录内执行（bin/ 需含 netwatch-server 与 victoria-metrics）"
  exit 1
fi
ok "交付包二进制完整（server / victoria-metrics / agent）"

command -v systemctl >/dev/null || { err "未检测到 systemd"; exit 1; }
ok "systemd 可用"

# ---------- 2. 时间同步 ----------
step "2/8 时间同步（chrony）"
if ! command -v chronyd >/dev/null 2>&1 && ! rpm -q chrony >/dev/null 2>&1; then
  run "$PKG install -y chrony"
fi
run "systemctl enable --now chronyd"
sleep 2
chronyc tracking >/dev/null 2>&1 && ok "chronyd 运行中" || warn "chronyd 未就绪，请人工确认时间同步"

# ---------- 3. 防火墙与 SELinux ----------
step "3/8 防火墙与 SELinux"
if command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
  run "firewall-cmd --permanent --add-port=8080/tcp"
  run "firewall-cmd --reload"
  ok "firewalld 已放行 8080/tcp"
else
  ok "firewalld 未运行（跳过放行）"
fi
if command -v getenforce >/dev/null && [ "$(getenforce)" = "Enforcing" ]; then
  warn "SELinux 为 Enforcing：如启动失败请执行
      semanage port -a -t http_port_t -p tcp 8080  （需 policycoreutils-python-utils）
      或临时 setenforce 0 验证"
else
  ok "SELinux 非强制模式"
fi

# ---------- 4. 目录与用户 ----------
step "4/8 安装目录与运行用户"
run "mkdir -p $ROOT/bin $ROOT/web $ROOT/vmdata $ROOT/data/agent-releases"
id netwatch >/dev/null 2>&1 || run "useradd -r -d $ROOT -s /sbin/nologin netwatch"
run "install -m 0755 $SRC/bin/netwatch-server $ROOT/bin/"
run "install -m 0755 $SRC/bin/victoria-metrics $ROOT/bin/"
[ -f "$SRC/bin/netwatch-agent" ] && run "install -m 0755 $SRC/bin/netwatch-agent $ROOT/bin/"
[ -d "$SRC/web/dist" ] && run "cp -r $SRC/web/dist $ROOT/web/"
run "chown -R netwatch:netwatch $ROOT"
ok "目录就绪"

# ---------- 5. VictoriaMetrics ----------
step "5/8 VictoriaMetrics 时序库"
run "sed \"s|/opt/netwatch|$ROOT|g\" $SRC/deploy/systemd/victoriametrics.service > /etc/systemd/system/victoriametrics.service"
run "chown -R netwatch:netwatch $ROOT/vmdata"
run "systemctl daemon-reload"
run "systemctl enable --now victoriametrics"
sleep 3
if curl -fsS -m 5 "http://127.0.0.1:8428/health" >/dev/null 2>&1; then
  ok "VictoriaMetrics 健康"
else
  err "VictoriaMetrics 未就绪，查看: journalctl -u victoriametrics"; exit 1
fi

# ---------- 6. PostgreSQL ----------
step "6/8 PostgreSQL 业务库"
if ! command -v psql >/dev/null 2>&1; then
  echo "  未安装，尝试 $PKG 安装（麒麟源自带 postgresql-server）..."
  run "$PKG install -y postgresql-server postgresql-contrib"
fi
ok "PostgreSQL 客户端就绪"
PGDATA="/var/lib/pgsql/data"
if [ ! -f "$PGDATA/PG_VERSION" ]; then
  run "postgresql-setup --initdb"
fi
run "systemctl enable --now postgresql"
sleep 2
# 放行 127.0.0.1 密码认证（麒麟默认 ident 需改为 md5）
HBA=$(su - postgres -s /bin/sh -c 'psql -tAc "show hba_file;"' 2>/dev/null | tr -d ' \r')
if [ -n "$HBA" ] && [ -f "$HBA" ]; then
  run "sed -i 's/^\(host.*127\.0\.0\.1\/32.*\)ident\(.*\)$/\1md5\2/; s/^\(host.*::1\/128.*\)ident\(.*\)$/\1md5\2/' $HBA"
  run "su - postgres -s /bin/sh -c 'pg_ctl reload -D $PGDATA' 2>/dev/null || systemctl reload postgresql"
  ok "pg_hba 已放行本机密码认证"
fi
# 创建用户与库（幂等）
PGPASS_FILE="$ROOT/.pg-pass"
if ! su - postgres -s /bin/sh -c "psql -tAc \"SELECT 1 FROM pg_roles WHERE rolname='netwatch'\"" 2>/dev/null | grep -q 1; then
  PGPASS=$(openssl rand -hex 12 2>/dev/null || head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')
  run "su - postgres -s /bin/sh -c \"psql -c \\\"CREATE USER netwatch WITH PASSWORD '$PGPASS';\\\"\""
  run "su - postgres -s /bin/sh -c \"psql -c \\\"CREATE DATABASE netwatch OWNER netwatch;\\\"\""
  echo "$PGPASS" > "$PGPASS_FILE" && run "chmod 600 $PGPASS_FILE && chown netwatch $PGPASS_FILE"
  ok "数据库与用户已创建（口令见 $PGPASS_FILE）"
else
  ok "数据库用户已存在（沿用）"
  [ -f "$PGPASS_FILE" ] && PGPASS=$(cat "$PGPASS_FILE") || PGPASS="netwatch"
fi
PGPASS="${PGPASS:-netwatch}"

# ---------- 7. 中心服务 ----------
step "7/8 NetWatch 中心服务"
ENROLL=$(openssl rand -hex 16 2>/dev/null || head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')
cat > /tmp/netwatch-server.service <<UNIT
[Unit]
Description=NetWatch center server
After=network-online.target postgresql.service victoriametrics.service
Wants=network-online.target

[Service]
Type=simple
User=netwatch
Group=netwatch
WorkingDirectory=$ROOT
Environment=NW_LISTEN=:8080
Environment=NW_DB=postgres://netwatch:${PGPASS}@127.0.0.1:5432/netwatch?sslmode=disable
Environment=NW_VM_URL=http://127.0.0.1:8428
Environment=NW_ENROLL_TOKEN=${ENROLL}
Environment=NW_BASE_URL=
Environment=NW_WEB_DIR=$ROOT/web/dist
Environment=NW_DATA_DIR=$ROOT/data
ExecStart=$ROOT/bin/netwatch-server
Restart=always
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=$ROOT
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
UNIT
run "mv /tmp/netwatch-server.service /etc/systemd/system/netwatch-server.service"
run "systemctl daemon-reload"
run "systemctl enable --now netwatch-server"
sleep 4
if curl -fsS -m 5 "http://127.0.0.1:8080/healthz" >/dev/null 2>&1; then
  ok "中心服务健康"
else
  err "中心服务未就绪，查看: journalctl -u netwatch-server"
  if [ "$(getenforce 2>/dev/null)" = "Enforcing" ]; then warn "SELinux Enforcing——尝试 semanage port -a -t http_port_t -p tcp 8080 后 systemctl restart netwatch-server"; fi
  exit 1
fi
IP=$(hostname -I 2>/dev/null | awk '{print $1}')
ok "管理后台: http://${IP:-<本机IP>}:8080"
ok "注册令牌（部署探针时使用，亦可在系统设置中修改）: $ENROLL"
ok "大屏: http://${IP:-<本机IP>}:8080/#/screen"

# ---------- 8. 部署自验证 ----------
step "8/8 部署自验证"
curl -fsS -m 5 "http://127.0.0.1:8080/healthz" >/dev/null && ok "中心服务健康检查通过"
curl -fsS -m 5 "http://127.0.0.1:8428/health" >/dev/null && ok "VictoriaMetrics 健康检查通过"
su - postgres -s /bin/sh -c "psql -tAc 'SELECT 1'" >/dev/null 2>&1 && ok "PostgreSQL 可连接"
echo -e "\n${C_G}========== 中心端部署完成 ==========${C_N}"
echo "下一步：
  1. 浏览器打开 http://${IP:-<本机IP>}:8080 → 初始化向导（建管理员/SMTP/线路清单）
  2. 系统设置中确认注册令牌后，用 install-agent.sh 部署 12 台探针
  3. 阿里云服务器探针的接入方式见 docs/04 §3.1
提示：本脚本可重复执行（幂等）；卸载：systemctl disable --now netwatch-server victoriametrics && rm -rf $ROOT"
