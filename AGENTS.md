# AGENTS.md — NetWatch 项目 AI 交接记忆

> 本文件是给 AI agent 的项目记忆。任何新的 AI agent 接手本项目前**必须完整阅读本文件**。
> 人类读者的文档在 `docs/`（01-06），本文件包含文档之外的会话级记忆：决议史、环境细节、踩坑记录。

---

## 0. 项目一句话

为单位（12 条联通宽带 × 12 个楼层子网，防火墙按源网段策略路由）自研的出口网络监控系统 **NetWatch**：楼层探针监测每条线路连通性/网速 + 阿里云外网探针监测官网可达性 + 告警（邮件/钉钉/企微）+ Web 管理后台 + 大屏。Go 单模块（agent + server）+ Vue3 前端 + PostgreSQL + VictoriaMetrics。

## 1. 当前状态（截至 2026-11-06）

**开发全部完成**，现场实施进行中。里程碑：M0 骨架 / M1 连通性+后台 / M2 测速+大屏 / M3 报表权限审计自更新 / M4 文档+演练预演 —— 全部 ✅。

**2026-11-05 正式中心端已上电**：全新专用麒麟 V10 服务器（10.8.14.200）原生部署完成（无 Docker，PG/VM 均本机 systemd），server 监听 **0.0.0.0:80**，开机自启，端到端验证通过。
**2026-11-05/06 现场单探针全功能测试完成（报告=docs/07）**：探针服务器 **10.8.14.201**（麒麟 V10 1C1G，root 密码 `[探针root口令见现场授权台账]`，用户授权 SSH）install-agent.sh 一键装探针并绑定"联通-测试线"（id=1，楼层"测试楼层"=10.8.14.0/23，网关 10.8.15.253，真实出口 HTTP 实测 ≈24.7Mbps）；22 项功能测试全过（四类告警演练/测速全链路/邮件+IM mock 推送/静默/RBAC/报表/CSV 导入/SSH 运维/WS 终端/内置监控/审计）；**发现并修复 6 项缺陷**（见 §7 坑 15-17、坑 4/11 补充与 docs/07 §3），修复**已 commit 未 push**。管理员已建：**admin / [初始管理员口令]**（代跑 setup 向导，用户可改密）。演练告警记录保留（全部 resolved）。viewer 账号（[观察者口令见现场台账]）保留。
**2026-11-06 行业网监测功能上线（已发布生产）**：中心对管理员配置 IP（10.5 网段）**仅 ICMP ping**（不做其他探测以免被封），独立引擎 internal/server/industry.go（settings 键 industry_monitors，指标 nw_industry_*，告警 industry_down P2 连续 3 次失败/删除收口），总览新卡+探测历史 type=industry；顺带修复 history rttAt 键 bug（monitor/industry 类 RTT 恒 0）。**重要：生产 server unit 原缺 CAP_NET_RAW，非 root 下所有中心 ICMP（含旧内→内监控 icmp 类型）都会 EPERM 失败——已补 AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_RAW 双能力并回环实测 0.23ms 打通**，模板（install-server.sh/systemd/）同步。
**2026-11-06 楼层 IP 扫描上线（已发布生产）**："监控展示→🔎 IP 扫描"独立一级页面（/scan，ScanView.vue，选楼层→扫描；应升级要求自线路配置页卡片迁出），中心对楼层 CIDR 逐 IP ping 单包（并发 64、单 IP 1s 超时、WaitGroup+channel 信号量，/24 实测 4.2s），格子墙一 IP 一格绿在线/灰离线（style.css .ip-grid/.ip-cell）；生产 /23 实测 510 格 8.4s 在线 19；internal/server/scan.go（scanTargets 纯函数+GetFloor 新增 store 函数+网段上限 /22 跳网络广播地址）；POST 天然 admin-only+自动审计。注意：禁 ping 主机（Windows 默认防火墙）显示离线；pingOnce 不吃 ctx 靠 Timeout 兜底。
**2026-11-06 登录 30 天免重复登录 + health API**：会话持久化到 PG sessions 表（Cookie Max-Age 与 exp 均 30 天，server 重启后 requireSession 内存 miss 回源 PG 恢复——**重启不再踢登录**），logout 双删；新增公开 GET /api/health（JSON：status ok/degraded、version、uptime_s、checks{db,vm}、time，Makefile 已注入 git describe 版本号到 agent/server）；大屏 /#/screen 与 /api/screen/overview 本就公开免登录（核实）。\n额外完成的二期功能：IM 告警通道（钉钉/企微）、双探针交叉验证、压测回放（cmd/loadgen）、SNMP 真实流量采集、探针零接触运维（SSH 凭据后台管理+网页终端）、外网组官网探测（含证书检测）、内→内内置监控（中心自探，Uptime Kuma 模式）、探测历史逐条查询（PRTG 风格，/history 页）。
**2026-11-06 监控大屏重设计（已发布到 10.8.14.200）**：ScreenView.vue 全量重写，NOC 深色科技风。布局=头部（状态呼吸灯+标题+正常/异常/盲区/告警四 KPI 卡+时钟星期中文）/主体（左=线路卡片墙 `auto-fill minmax(300px,1fr)`+`align-content:start`，修复旧版单线路卡片被 1fr 拉成巨块；右=24vw 信息栏：外网探测+内→内+行业网+当前告警，这些数据 overview API 早就返回、旧版全没展示）/页脚（未绑定探针警告+连接失败提示+上次刷新时间）。卡片内新增 Uptime Kuma 风格 30 格心跳条（ProbeStatus.recent，时间正序左→右渲染）与测速对契约带宽百分比+进度条（≥85 绿/≥70 黄/否则红）；down/floor_down/blind 卡片脉冲发光。验证：headless_shell 截图 1920×1080（注意 **大屏地址必须 `/#/screen` hash 路由**，直打 /screen 会落登录页）。本机无 playwright/chromium CLI，用 /root/.cache/ms-playwright/chromium_headless_shell-1181/chrome-linux/headless_shell --screenshot 直接截图即可。
**2026-11-06 前端信息架构重排（已发布到 10.8.14.200）**：侧栏分「监控展示 / 配置管理」两组；探针拆为 探针状态(/probes，只读+15s 刷新) 与 探针管理(/probes/manage)；系统设置由 445 行单页拆为页内 6 Tab 子路由（web/src/views/settings/：notify 命令/alert 策略/probe 探测/collect 采集测速/report 报表/account 用户审计），审计日志归位 account、静默窗口自告警中心移入 alert；告警中心纯展示；**配置管理组改二级菜单（2026-11-06 二次调整）**：线路配置/探针管理为一级（高频），系统设置父项下挂 6 个二级子页（低频，展开态派生自路由 settingsOpen=computed(path.startsWith)，无三级；**2026-11-06 三次调整：加顶部标题栏**（左=分组/页面面包屑 TITLES 映射，右=大屏按钮自侧栏迁入+当前用户头像，用户来自新增 GET /api/admin/me——App 在登录页就挂载，me 须 watch(showLayout) 拉取；面包屑前缀匹配须排除 '/' 自身）。后端仅加 me 接口；生产发布仅换 web/dist（静态按请求 ServeFile，**web-only 发布无需重启 server**）。（注：当时并行的 egress_ip Go 改动已由后续提交 4874599 完成落地，go build 恢复通过。）
**2026-11-06 数据库文档成册（docs/09）**：结构/指标/运维配方详见该册；生产 settings 表仅 9 键（令牌未落库，实际生效=unit 环境变量）。
**2026-11-06 探针部署文档成册（docs/08）**：《08-探针部署与配置手册》覆盖原理/准备/单台与批量安装/绑定分组/SSH 凭据/探测与测速配置/外网组/升级/验证清单/排查；docs/04 §3 瘦身为速查并指向它，docs/04 旧端口 8080 标注生产实值 :80、旧界面引用全部改为重排后路径。
**2026-11-06 楼层配置页与线路配置页样式统一（已发布 10.8.14.200）**：FloorConfigView 原标题下独立说明段落 + 卡片底部说明段落使列表比线路配置页多出两段高度——改为全站统一模式（同 ScanView/ProbesManageView）：`d-flex align-items-center mb-3` 包 `h4 mb-0` + 行内 `text-secondary small ms-3` 说明（"楼层顺序即总览/大屏展示顺序"并入行内说明；删除警告本就在删除确认框中），卡片=头部+表格无尾注。验证：两页表格行高本就同源 CSS（.nw-table 上下 .68rem padding）均为 54px，headless_shell 截图逐像素核对分隔线坐标确认对齐；web-only 发布（cp dist，未重启 server）。

**零接触开通（2026-11-06）**：internal/server/provision.go——「探针管理→＋新建探针」弹窗输入全新服务器 IP/账号/密码（一次），后台异步任务（内存态 provJobs，POST /probes/provision 秒回 job_id、GET /probes/provision/{id} 轮询）：SSH 免密（NW_SSH_PUBKEY_FILE=/opt/netwatch/.ssh-center.pub）→ 主机名 probe-<尾段> → 二进制上传（**sha256 校验**）→ systemd → 等注册（源 IP 匹配，≤60s）→ 等出口 IP（引擎快照，≤90s）→ 出口 IP 匹配 lines.public_ip 绑定（无匹配自动建线=运营商-出口IP，按楼层 CIDR 归属楼层）。**坑：agentBinaryPath 取 server 同目录（/opt/netwatch/bin）——发布时 netwatch-agent 也要同步更新到该目录，仓库 bin/ 不等于部署 bin/**（本次实测踩坑：误判为二进制损坏排查半天）。10.8.14.202 实测 20s 全通过（含后台 SSH 运维双通道）。**2026-11-06 拓扑澄清（用户明确）**：全部探针位于 10.8.14.0/23 管理网段，防火墙按源 IP 策略路由给每台探针分配各自出口线路——**出口 IP ↔ 线路 ↔ 楼层是唯一识别链**；按探针源 IP/楼层 CIDR 猜楼层的逻辑已删除（该拓扑下必然错），楼层只随线路；开通弹窗支持"预期线路"可选校验（实际出口不符→当场告警，装机时发现策略路由配错），同线路已有探针也会提示。**立即刷新按钮（2026-11-06）**：探针无入站端口，刷新指令走心跳应答夹带——POST /probes/refresh（空=全部）→ Engine.refreshSet → 心跳应答 refresh_egress:true → agent 立即重探出口三件套并马上回报（实测 7s 到达），探针日志留痕 "egress refresh requested"；状态页/管理页均有 🔄 立即刷新按钮（点击后 30s 内 3s 快速轮询）。**12 线路正式拓扑落地（2026-11-06，用户两表汇总确认）**：12 探针(1:1)12 线路(1:多)楼层段——
联通专线线路100M(机房)、联通线路1=1-3楼+8楼-1(10.8.30.0/25)+10楼、线路2=4楼、3=5楼、4=6楼、5=7楼、
6/8暂未分配、7=9楼、9=104号院38+39段、10=wifi34+35段(实际10.8.34.0/23按/24拆扫)、11=8楼-2(/25)；
楼层实体 13 个（8楼拆两半、wifi/104 按段拆），管理网段(10.8.14.0/23)保留不挂线。
**线路↔楼层多对多（2026-11-06）**：line_floors 关联表替代 lines.floor_id 单值；LineRow.Floors[]/floor_ids 入参/
FloorName 聚合展示（"1-3楼、8楼-1、10楼"），线路弹窗楼层复选。**未绑定探针自动匹配**：出口 IP 命中某线路
public_ip 即自动绑定+审计 auto-bind（egress-identity 闭环——探针上线/改路由自动归位；未命中不建线）。
**开通预期线路增强**：预期线路出口为空时，探到的出口直接登记为该线路身份并绑定（不另建重复线）。
**收敛冲突（用户并行操作）**：用户手工建的"联通-2/3号线"（带真实出口+绑定）改名并入规划命名，空壳线删除。
注意：专线(218.28.242.106) 现无探针（202 被用户改路由到 4楼线）。**命名规则（2026-11-06 用户确认）**：显示名称=probe-<序号>（对应线路表 probe-1~12），OS 主机名=probe-<IP尾段>（probe-201/202/203/204，与名称解耦）——provision 主机名已固定取 IP 尾段（名称仅随注册上报/后台可改），install-agent.sh 同步，四台 systemd 单元 -name 已对齐（防重注册覆盖显示名）；截至当前 4 台在线（线路1-4），线路4 出口 115.60.2.222 已由预期线路流程自动登记。
**出口变更自愈（2026-11-06）**：心跳对账升级为 egress-identity 语义——绑定线路 IP 为空=首次登记回填；出口与绑定线路不符=自动改绑到出口对应线路（无匹配自动建线，审计 egress-rebind），**绝不回写原线路 public_ip**（防污染线路身份）；探针探测不到出口时如实清空 egress（agent+引擎双侧，保留旧值会误导匹配——实测踩坑：用户调整 201 策略路由未放通公网期间旧值残留）。

**现场实施操作日志（2026-11-06，用户要求留档）**：docs/操作日志-现场实施.md——已完成操作时间线、系统基线（4 探针在线/12 线路/13+1 楼层）、probe-5~11 逐台开通手册（IP/名称/预期线路对照表+核对清单）由用户按此逐台操作；全部开通后做收尾核对（12 线全景/扫描抽测/审计/多探针演练）。**开通完成（2026-11-06 15:42，用户手动逐台开通）**：12/12 探针在线、12/12 线路出口登记、名称↔线路零偏差、收尾核对+实弹演练（probe-5 阻断→P1→恢复收口）全部通过——见操作日志 §二/§四。剩余：docs/05 多探针演练、真实 SMTP/IM、上行契约带宽、7×24 试运行。

剩余现场实施事项：
1. 12 台楼层探针 VM（`deploy/install-agent.sh` 单台 / `batch-install-probes.sh` 批量，**先绑定线路再启用**）；已在 10.8.14.201 真实麒麟主机全流程实测通过；
2. 阿里云服务器装探针切外网组（中心可达三方案见运维手册 §3.1：公网映射/VPN/FRP）；
3. 真实 SMTP/钉钉/企微/SNMP 联调（本次以本机 mock 靶 8025/18099 验证过载荷链路）；
4. 多探针规模演练（交叉验证/P2 冲突等在多探针维度的场景）+ 按 docs/05 完成 9 项演练 + 7×24 试运行 + 验收签字；
5. `make dist` 重建交付包（本机无 VM 二进制缓存需联网）+ 修复 commit push 远端。

## 2. 需求决议史（用户确认过的所有决策，不得擅自更改）

1. **2026-10-25 初版需求**：12 条联通宽带策略路由按楼层子网分流；监测每楼每线连通性+网速；异常报警；虚拟机探针数量不限但要统一部署调度；Web 管理界面+大屏+邮件报警。
2. **UI 参考 Uptime Kuma**（MIT，Vue3+Bootstrap5+Chart.js 同栈，可复用代码）。核实过协议合法。
3. **测速方式**：探针直接对互联网测速（Ookla 协议实现 speedtest-go + HTTP 大文件兜底），不自建测速节点；全局串行错峰防扰民。
4. **线路清单/SMTP 不做前置条件**：部署后在管理后台填写（首次启动向导）。
5. **探测目标后台自由配置**；默认 3 个公共 DNS ping + qq/baidu TCP/HTTP（多目标投票防单目标误判）。
6. **部署方式**：原生 systemd 为主（单业务服务器不需要 Docker），Docker Compose 保留为备选。
7. **探针零接触运维**：12 台批量装完后不再逐台登录——探针改名、SSH 凭据（root 密码统一 CSV 导入中心，AES-256-GCM 加密落库）、测试连接/重启服务/远程命令/网页终端全部在后台完成。
8. **网页终端**：用开源 xterm.js + gorilla/websocket 实现阿里云风格浏览器 SSH（仅管理员+审计）。
9. **外网探测官网**：阿里云服务器装同款探针切"外网组"，ping 官网域名 + HTTPS 访问（状态码+证书剩余天数）；全部外网探针失败才 P1"网站从外网不可达"。
10. **2026-10-31 PRTG 借鉴功能评估后全部不做**（用户明确决议）：告警确认 ACK、通知升级 Escalation、依赖抑制、汇总 Digest、环比对比、SLA 报表增强、自愈动作、AD/LDAP 登录、内网自动发现、自定义脚本监控、NetFlow、集群高可用——均维持现状，系统保持轻量。**后续不要重复提议这些功能**；若用户主动要求某项再实施。

## 3. 代码地图

```
cmd/agent/        探针入口（单二进制，systemd 部署在楼层 VM/阿里云）
cmd/server/       中心端入口（HTTP: 探针接入+管理API+大屏API+前端托管）
cmd/loadgen/      压测工具（模拟 N 探针/回填历史/实时流，身份持久化 /tmp/loadgen-state.json）
cmd/sshmockd/     测试用 SSH 服务器（密码认证+exec+PTY假shell，验证远程运维链路）
internal/proto/   agent↔server 线上协议（ProbeConfig/Sample/SpeedTask/UpgradeTask）
internal/agent/   探针：probes.go(四类探测器+源IP绑定+证书提取) runner.go(调度+jitter)
                  identity.go(注册/凭证/configWatch 30s轮询) reporter.go(批量上报+断网缓存补传)
                  speed.go(双引擎测速) upgrade.go(自更新:下载→sha256→原子替换→exec重启) sysinfo.go
internal/server/  config.go store.go(全部SQL) tsdb.go(VM写入+查询) alert.go(告警引擎核心)
                  speedsched.go report.go(日报周报) snmp.go notify.go(DingTalk/企微)
                  sshterm.go(网页终端WS桥) sshops.go(SSH运维+AES加密) api_agent.go api_admin.go
                  api_screen.go + *_test.go(丢包中位数/CSV/decideLine/SSH进程内服务器测试)
web/              Vue3 前端（src/views: Setup/Login/Dashboard/Lines/LineDetail/ProbesStatus/
                  ProbesManage/SshTerm(xterm.js)/Alerts/History/Reports/Screen +
                  views/settings/: SettingsLayout+Notify/Alert/Probe/Collect/Report/Account 六 Tab +
                  components: StatusBadge/HeartbeatBar；utils.js 公共小工具）
deploy/           install-server.sh(一键中心部署:环境检查+自动装PG/VM/chrony+自验证,CHECK_ONLY=1预检)
                  install-agent.sh(一键探针:对时+连通性+安装+注册确认) batch-install-probes.sh
                  systemd/*.service docker-compose.yml server.Dockerfile
docs/             01调研 02设计规格 03开发计划 04运维手册 05演练与验收 06用户手册
dist/             make dist 交付包（netwatch-0.2.tar.gz + sha256）
```

**数据流**：agent 探测 → POST /api/agent/metrics（批量）→ server Ingest（写 VM + 更新告警引擎内存态）→ evaluate（5s 周期）判定 → 告警（PG 持久化 + SMTP/IM 推送）。配置反向：agent 每 30s GET /api/agent/config（按探针分组 internal/external 返回不同配置 + 附加测速/升级任务）。

## 4. 构建/测试/打包（本机命令）

```bash
export PATH=$PATH:/usr/local/go/bin GOPROXY=https://goproxy.cn,direct   # Go 1.23.12 装在 /usr/local/go
cd <仓库根>            # 必须先 cd，shell 的 cwd 会漂移（沙箱特性，见 §7）。正式机=/root/network-monitor（git clone）；旧沙箱演示机曾用 /root/applications/network-monitor
make build        # agent+server+web（web 用 npmmirror）
make test         # go test ./...
make dist         # 交付包 dist/netwatch-0.2.tar.gz
# 单独：CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/netwatch-server ./cmd/server
#       cd web && npm run build
```

**重要流程约定：改了 server 代码后必须重启演示环境的 server 进程才生效**（改了 n 次忘记重启导致"改了没效果"的误判，见 §7 坑 1）。

## 5. 运行环境（当前运行态）

### 5a. 正式中心端（2026-11-05 部署，本机）

- **机器**：全新专用服务器，麒麟 V10 (Halberd) x86_64，2C/2.8G/17G（内存偏紧，VM 缓存已限 20%），IP **10.8.14.200**，SELinux Disabled，firewalld 已放行 80/tcp。**无 Docker**。
- **项目路径**：`/root/network-monitor`（git clone，main 分支）；**部署根**：`/opt/netwatch`（bin/ web/ vmdata/ data/，属主 netwatch 系统用户）。
- **服务（全部 systemd enabled 开机自启）**：`postgresql`（yum PG **10.5**，/var/lib/pgsql/data，hba 127.0.0.1 改 md5）· `victoriametrics`（v1.102.1 单二进制，`-memory.allowedPercent=20 -retentionPeriod=90d`，:8428）· `netwatch-server`（**0.0.0.0:80**，非 root 绑 80 与 ICMP 靠单元内 `AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_RAW`）。
- **env（在 /etc/systemd/system/netwatch-server.service）**：`NW_LISTEN=:80`、`NW_DB=postgres://netwatch:<随机口令>@127.0.0.1:5432/netwatch?sslmode=disable`（口令在 `/opt/netwatch/.pg-pass` 0600）、`NW_VM_URL=http://127.0.0.1:8428`、`NW_ENROLL_TOKEN=<随机>`（`/opt/netwatch/.enroll-token` 0600）、`NW_WEB_DIR=/opt/netwatch/web/dist`、`NW_DATA_DIR=/opt/netwatch/data`。
- **本机工具链**：Go 1.26.10（/usr/local/go，go.mod 实际要求 1.26 而非旧记的 1.23）、Node 20.18.1（/usr/local/lib/nodejs，软链 /usr/local/bin）。国内源：GOPROXY=goproxy.cn、npm=npmmirror、Go/Node 包=mirrors.aliyun.com、GitHub 走 `https://ghfast.top/` 前缀（直连不通）。
- **待办（用户侧）**：setup 向导未跑（`/api/admin/setup` 返回 initialized:false，管理员未建）；探针 0 台待用户配置；真实 SMTP/IM/SNMP 未配。本次部署**未 git push**。

### 5b. 旧沙箱演示环境（另一台机器，仅存档参考）

- 曾用路径 /root/applications/network-monitor（2026-11-02 由 /root/network-monitor 迁移），端口 18003，依赖 docker 容器 nwtest-pg(PG16)/nwtest-vm(VM 1.102.1)，探针 agent1(systemd)/agent2(uid 1234)，账号 admin/[测试口令]。以下踩坑经验仍适用本机：探针身份丢失自动重注册、会话存内存重启即失效、/tmp 属主与孤儿行清理等。

## 6. 数据模型与指标速查

**PG 表**：floors / line_floors（线路↔楼层多对多）/ lines（public_ip 由探针自动探测回填：探针每 5min 访问 IP 回显服务、随心跳上报、中心对账纠偏，手工值仅作探测全挂时兜底）/ probes（含 name、grp、ssh_*、pending_version）/ alerts / silences / reports / audit / agent_releases / users / settings。
**settings 键**：agent_config（内网组探测配置）、agent_config_external（外网组）、internal_monitors、industry_monitors（行业网监测，仅 ICMP）（内→内内置监控）、smtp、alert_settings、speed_settings、report_settings、notify_channels、snmp_settings、agent.enroll_token。
**VM 指标**：nw_ping_{rtt_ms,ok,loss_pct}{class=gw|public}、nw_tcp_*、nw_dns_*、nw_http_{ttfb_ms,ok,code,cert_days}、nw_speed_{down,up}_mbps、nw_snmp_{in,out}_bps{line}、nw_monitor_{ok,rtt_ms}{monitor,type}（内→内）。标签：probe/line/target/monitor。、nw_industry_{ok,rtt_ms}{industry}（行业网监测，中心仅 ICMP）
**协议新增（2026-11-06）**：Heartbeat.PublicIP/IPISP/IPGeo/IPv6/CPUPct/MemPct/DiskPct/AgentTS/SysInfo、ProbeConfig.IPEchoURLs（回显服务池，空=探针内置默认 ip.3322.net/members.3322.org/v4.ident.me/myip.ipip.net）——只增字段，新旧兼容。线路出口属性（public_ip/isp/geo）全部探针自动探测对账回填（手工值仅回显全挂时兜底；lines 手工更新对这三个字段空值不覆盖）；probes.sysinfo JSONB 存资产（心跳补报，不重注册也能刷新）；P2 probe_resource=探针内存/磁盘>90% 连续 3 心跳。证伪：DNS 出口 IP 检测不可用（公共递归返回自身出口 123.6.27.94≠线路出口）。
**探针名称**：RegisterReq.Name（install-agent.sh 第 4 参数/-name 传入），注册时落 probes.name；告警邮件/探针列表/未绑定区均显示名称。**注意**：外网组探针不计入 unbound 警告（overviewData 已过滤）。

## 7. 踩坑记录（新 agent 必读，避免重复排查）

1. **改 server 代码后忘记重启进程**→表现为"新功能 404/行为不变"。正式机用 `systemctl status netwatch-server` 看启动时间（改代码后需 `make build && install -m 0755 bin/netwatch-server /opt/netwatch/bin/ && systemctl restart netwatch-server`）。
2. **Bash 工具的 shell cwd 会漂移**：相对路径 `./bin/...` 偶发 "command not found"（exit 127）。**永远先 `cd` 仓库根（正式机 /root/network-monitor）或用绝对路径**。
3. **pkill -f 匹配会把执行 shell 自己也杀掉**（命令行含相同字符串）；用 `ps -eo user,pid,cmd | grep 'netwatch-agen[t]'` 后按 PID kill。
4. **gosnmp**：返回的 OID 带**前导点**（`.1.3.6...`），OctetString 是 `[]byte` 不是 string，Counter64 值就是 uint64（gosnmp.Counter64 是常量不是类型）。
5. **VM 即时查询**（/api/v1/query）有 ~30s latencyOffset，刚写入的数据查不到属正常；export/range_query 不受影响。**回填数据后立即生成报表可能查不到**（索引未刷新），稍等再生成即可。
6. **agent2 状态目录**（/tmp/nw-agent2）若属主不是 1234 会 permission denied 循环重注册；/tmp 偶发被清导致身份丢失→自动重注册新 ID（ resilience 生效），旧 DB 行需手动删除。
7. **备份/迁移注意**：SSH 凭据密钥在 `data/ssh-master.key`（0600），丢了已存密码全部解不开。
8. **演练用 iptables 阻断目标**做"线路中断"（OUTPUT 链 -d 目标 -j DROP）；用 `-m owner --uid-owner 1234` 做单探针分歧；用 `tc qdisc add dev ens3 root netem loss 40%` 做降质。演练完务必删规则。
9. **agent 心跳/配置轮询都是 30s 周期**，改配置后等 ~30s 生效；测速调度 tick 15s、告警评估 5s、报表调度 30s。
10. **loadgen 探针 hostname 前缀 loadgen-**，清理时按此前缀删 DB 行。
11. **内置监控（monitor.go）的探测必须用独立超时**（`context.WithTimeout(context.Background(),...)`），不能从 reload ctx 派生——配置热更新取消旧代 ctx 时，在途探测的 context canceled 会被误记为探测失败，产生间歇性假 down（真实踩过：中心端Web 时好时坏，根因即此）。同模式适用于一切"热更新 ctx + 网络探测"组合。
12. **探针时钟漂移**：样本时间戳由探针产生，探针时钟快 >2min 会污染判定窗口（漏报）。Ingest 已内置钳位（快于服务器 >2min 的样本 TS 改为服务器当前时间，日志 `clock skew`）；历史回填（loadgen -backfill）发过去时间戳不受影响。根因解法是给全部 VM 配 NTP。
14. **探测历史（history 接口）**：数据源是 VM /api/v1/export（NDJSON 逐行 JSON），经 TSDB.Export 解析；monitor 类型无 probe 标签、按 monitor 名过滤；HTTP 的 code/cert 是独立序列（nw_http_code / nw_http_cert_days），按 (target|ts) 关联回记录。
13. **沙箱单机测试的覆盖边界**（详见与用户的测试局限性讨论）：策略路由前提、前端浏览器渲染、真实 SMTP/钉钉/企微/防火墙 SNMP、12 台真实 VM 系统差异均未实测——现场部署后按 §1 剩余事项逐项验证，重点先跑三个"测试"按钮。
15. **全功能 API 测试**（/tmp/functest.py 47 用例 + /tmp/functest2.py 18 用例，2026-10-31 全 PASS）：第一轮修 8 类（setup 顺序、重名拒绝、CSV 表头误判、ssh_port 钳位、assign 校验、limit 钳位、**版本号路径穿越**、删用户会话残留）；第二轮修 6 类（内置监控项重名/空名/空目标/非法类型、**重名用户静默提权**（CreateUser ON CONFLICT DO NOTHING 需查 RowsAffected）、history PromQL 注入转义、SMTP 密码明文回显（GET 掩码 __SET__ / PUT 空或占位符保留原值）、静默绑不存在线路 500、名称空白落库）。**连锁事故记录：提权 bug 使 uexist 成为第二 admin → "最后一个管理员"守卫失效 → 测试删掉了真 admin → 演示环境经 DELETE FROM users + setup 向导恢复**。教训：新加接口必须同步加回归用例；测试脚本对"已有数据"要幂等。
16. **agent 的 loadIdentity 曾从未被调用**（2026-11-06 现场修复）：此前探针每次重启都重新注册新 ID（旧行变盲区+孤儿行、绑定/凭据全丢）——2026-11-02 迁移记录中"两探针身份丢失"的真实根因就是它，不是迁移导致。凡"身份/状态应持久复用"的启动路径，务必确认加载函数真的被调用。
17. **lossStats 中位数退化**（2026-11-06 现场修复）：每目标 ≥10 样本门槛若各目标独立成立，启动期坏目标先凑满时中位数=该目标 100% → 瞬态降质误报；门槛必须**全部目标同时成立**（回归用例 TestLossStatsAsymmetricSampleCounts）。同批修复：降质与 P1 中断不叠加（outageActiveLocked 并入中断事件）、speed 调度手工触发与周期开关解耦（runOnce）、契约清零后 speed_low 收口、内置监控 reloadLoop **不得无条件换代**（否则 VM 写入全被 context canceled 打断——坑 11 的教训同样适用于"写库路径"，配置指纹变化才换代）。
19. **Vue 组件不能用内联 template 字符串**（2026-11-06 实测踩坑）：Vite 生产构建用 runtime-only Vue（无模板编译器），`{template: \`...\`}` 形式的组件**渲染为空且构建不报错**——一律用 SFC 文件。同批交互约定：所有"新增"配置（线路/楼层/探测目标/监控/行业网 IP）统一"点击新增→弹窗→确定"（ModalBox/TargetList 组件），行内表单与 textarea 不要再引入。
18. **现场演练配方（单探针，10.8.14.201 实测有效）**：线路中断=`iptables -I OUTPUT` 三条（ESTABLISHED 放行 / 10.8.0.0/16 放行 / DROP，恢复用预存 iptables-restore）；降质=`tc qdisc replace ... prio bands 2 priomap 1×16` + `parent 1:2 netem loss 40%` + u32 过滤放行 10.8.0.0/16（**必须豁免网关与中心上报方向**，否则楼层中断先触发搅局）；网关阻断=`-d 10.8.15.253 -j DROP`。单探针停止时 line_down(~45s) 先于 blind(60s) 属设计行为。测速注意：114.114.114.114 对该线路 ICMP 不通（坏目标，投票已消化）、Ookla 节点速度可能远低于线路真实值（用 curl HTTP 大文件交叉验证）、dldir1.qq.com 优先解析 IPv6。

## 8. 常用操作配方

```bash
# ===== 正式中心端（本机 10.8.14.200，全部 systemd 管理，勿用手动后台进程）=====
systemctl restart netwatch-server victoriametrics   # 重启服务；日志 journalctl -u netwatch-server -f
# 改代码发布流程：cd /root/network-monitor && make build（web 改动需 cd web && npm run build）
#   → cp -r web/dist /opt/netwatch/web/ && install -m 0755 bin/netwatch-server /opt/netwatch/bin/ && systemctl restart netwatch-server
# 数据库口令 / 注册令牌：cat /opt/netwatch/.pg-pass  /opt/netwatch/.enroll-token（均 0600）
# 备份：PG 用 pg_dump（su - postgres -c 'pg_dump netwatch'）；data/ssh-master.key 必须单独备份（见坑 7）
# 探针安装（后续）：用 deploy/install-agent.sh，指向 http://10.8.14.200（80 端口）+ .enroll-token 值

# ===== 以下为旧沙箱演示机（另一台机器）配方，仅存档 =====
# 重启中心端（演示）—— 先 ps 精确 kill 旧 PID（勿用 pkill -f，见坑 3）
cd /root/applications/network-monitor && NW_LISTEN=0.0.0.0:18003 NW_DB='postgres://netwatch:netwatch@127.0.0.1:5433/netwatch?sslmode=disable' NW_VM_URL='http://127.0.0.1:8428' NW_ENROLL_TOKEN=test-token NW_WEB_DIR=/root/applications/network-monitor/web/dist NW_DATA_DIR=/root/applications/network-monitor/data ./bin/netwatch-server > /tmp/nw-server.log 2>&1 &
# 重启 agent2（外网组，uid 1234）
cd /root/applications/network-monitor && NW_SERVER=http://127.0.0.1:18003 NW_ENROLL_TOKEN=test-token NW_STATE_DIR=/tmp/nw-agent2 setpriv --reuid 1234 --regid 1234 --clear-groups --inh-caps=+net_raw --ambient-caps=+net_raw ./bin/netwatch-agent > /tmp/nw-agent2.log 2>&1 &
# agent1 由 systemd 管理：改端口/参数需 sed /etc/systemd/system/netwatch-agent.service 后 daemon-reload + restart
# 登录取会话（Cookie 存 /tmp/nw-ck，后续请求 -b /tmp/nw-ck；会话鉴权用 Cookie nw_session，非自定义头）
curl -s -c /tmp/nw-ck -X POST http://127.0.0.1:18003/api/admin/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"[测试口令]"}'
# 压测回填 / SNMP mock / SSH mock / IM webhook mock
cd /root/applications/network-monitor && ./bin/loadgen -server http://127.0.0.1:18003 -token test-token -probes 12 -backfill 7
python3 /tmp/mock_snmp.py &            # udp 1161
/tmp/sshmockd 2222 root 'test-pass' &
python3 /tmp/mock_webhook.py &         # http 18099（notify_channels 的钉钉/企微测试目标）
```

## 9. 告警语义速查（判定逻辑在 internal/server/alert.go evaluate）

| 级别 | kind | 触发 |
|---|---|---|
| P0 | blind | 探针心跳 >60s |
| P1 | line_down | 绑定线路的全部有效探针（在线+网关OK）报公网全断（投票） |
| P1 | floor_down | 网关连续 3 次失败 |
| P1 | site_down | 外网组全部探针 HTTPS 失败（投票） |
| P2 | monitor_down | 内→内内置监控连续 3 次失败（中心自探，恢复即解除；目标删除自动收口） |
| P2 | industry_down | 行业网监测：中心对配置 IP 仅 ICMP ping，连续 3 次失败（恢复即解除；目标删除自动收口） |
| P2 | degraded/probe_conflict/speed_low/site_conflict/cert_expiring | 见代码 |

通用机制：静默窗口（30s 缓存）、恢复通知、迟滞、按级别收件组、IM 恒推 P0/P1。降质=按目标丢包**下中位数**（抗单坏目标）。状态汇总 statusOrder 确定性顺序（degraded/speed_low 同 rank）。

## 10. API 路由速查

- `/api/agent/*`：register、config（按组）、metrics、heartbeat、download/{id}（自更新，鉴权 X-NW-Agent/X-NW-Secret）
- `/api/admin/*`：bootstrap/setup/login、overview（lines+unbound+external_probes+external_status+internal_monitors 三视角）、floors/lines CRUD+CSV import、floors/{id}/scan（楼层 IP 扫描：中心并发 64 逐 IP ping 单包，/24 约 4s，仅 admin，网段上限 /22）、probes（列表/assign/group/meta/ssh: test|restart-agent|exec|terminal(WS)/import）、alerts、silences、reports(+generate/export.csv)、settings(+smtp/test、notify/test)、users、audit、agent-releases(+upgrade)、speedtest/run、snmp/ifaces、metrics/query_range（VM 代理）、history（PRTG 风格逐条探测记录，type=ping/tcp/dns/http/monitor/speed + probe/target/start/end/limit）
- `/api/screen/overview`（公开只读大屏）
- 角色：admin 全权限；viewer 只读（非 GET 403；WS 终端显式 403）

## 11. 文档索引

**2026-11-06 文档精简决议（用户明确要求）**：用户文档只保留两册——`docs/部署手册.md`（中心端启动/部署/升级 + 探针部署全流程，合并原 04+08）与 `docs/数据库表说明.md`（PG 10 表 + VM 指标 + 增长保留，取自原 09 结构部分）。原 01~09 九册已删除，**全文可从 git 历史找回**：`git show <删除提交>^:docs/<原文件名>`；其知识精华已全部并入本文件 §14，此后新增运维知识直接记入 AGENTS.md，不再新建用户文档。README 验证记录已压缩为摘要（完整逐条历史同样在 git）。

## 12. Git 与发布

- 仓库：`git@github.com:guojiaxian/network-monitor.git`（main 分支），2026-10-31 首推（v0.2 全量，75 文件）。
- .gitignore 覆盖：bin/、dist/（make dist 重建）、data/（**含 ssh-master.key 严禁入库**）、web/dist、web/node_modules。
- agent1 由 systemd 管理（/usr/local/bin + /var/lib/netwatch-agent，2026-11-02 迁移检查后 id e578107e47e11d94，绑定联通1号线）；身份目录丢失即自动重注册新 ID，旧行手动删。
- 发布流程：改代码 → make test → make dist → git commit/push。

## 13. 给下一个 AI agent 的行为约定

- 代码注释、日志、UI 文案用**中文**；新功能遵循现有模式（设置进 settings 表+白名单、告警进 kindSeverity/kindTitle、前端加卡片区块）。
- 任何改动：`go build ./... && go vet` 通过 → 重建 bin → 重启 server → 验证 → **验证记录写入本文件 §1/§14**（2026-11-06 起 README 验证记录已压缩，不再逐条追加）。
- 涉及协议（proto/）改动需考虑新旧探针兼容（字段只增不改语义）。
- 用户的新需求先对照 §2 决议史，避免推翻已确认决策；重大方案先问再动。
- 演示环境的测试数据（探针行、告警）可以清理重建，但 **admin 账号、联通1号线、外网组配置**是演示骨架，保持可用。

## 14. 被精简文档的知识记忆（2026-11-06 文档精简，原 01~09 册精华）

> 原文找回：`git show <删除提交>^:docs/<原文件名>`。本节替代被删文档，作为运维知识的唯一记忆位置。

### 14.1 故障处置速查（原 04§6，告警处置）

| 现象 | 处置 |
|---|---|
| P1 线路中断（网关正常） | 宽带线路/出口故障：总览确认全部公网目标失败 → 拨线路商报障（附告警时间现象）；恢复自动发恢复邮件 |
| P1 楼层中断（网关不通） | 楼内故障：查楼层交换机/网关供电/上联口；**勿报线路商** |
| P0 监控盲区 | 探针 VM 宕机或与中心失联：查 VM 与网络；计划维护先建静默 |
| P2 降质/带宽不达标 | 观察 2 个统计窗口 + 线路详情曲线再报障；高峰期可调测速时段 |
| P2 内网服务不可达 | 查该服务存活/端口；计划停机先静默或删目标 |
| 邮件收不到 | 通知渠道→测试邮件；`journalctl -u netwatch-server | grep "send mail"`；查单位邮件网关拦截 |
| 时序库磁盘增长快 | 调 VM `-retentionPeriod` 重启生效 |

### 14.2 安全基线与钉钉/企微注意（原 04§7）

- 钉钉机器人若开"关键词过滤"，关键词设为 `NetWatch`（消息标题均含），或改加签模式（SEC 密钥）；
- 建议为探针建专用运维账号而非 root，密码定期在后台轮换；远程命令仅管理员且全审计；
- 管理口令强口令；观察者账号发值班人员，人员变动及时在"用户与审计"删除。

### 14.3 管理员账号与密码重置（原 04§8）

- **系统无默认账号密码**（首访进向导自建，刻意设计）；
- 唯一管理员忘密码：`su postgres -c "psql netwatch -c 'DELETE FROM users;'"`（Docker：`docker exec netwatch-pg psql -U netwatch -c ...`）→ 刷新页面走向导重建；**只影响登录，监控数据/配置/告警历史全保留**。

### 14.4 应急联系模板（原 04§9，各单位自行登记）

线路中断→联通报障电话（P1 即时）；楼内故障→单位网络组（P1 即时）；监控系统自身→系统管理员（30 分钟内响应）。

### 14.5 故障演练与验收（原 05；剩余现场事项 #4 仍按此执行）

**9 个演练场景**（步骤→预期）：
1. 线路中断：拔纤/防火墙禁对应策略路由 → ≤30s P1"线路中断"邮件，恢复收恢复邮件（含中断时长）；
2. 楼层网关断（不动宽带线）→ P1"楼层中断"，文案与①明确可区分；
3. 探针关机 70s → ≤60s P0 盲区（线路紫色），恢复自动通知；
4. `tc qdisc add dev <网卡> root netem loss 30%` 保持 6 分钟 → P2 降质（单目标偶发丢包不触发）；删 qdisc 恢复；
5. `tc ... tbf rate <契约70%以下>` 触发 2 次测速 → P2 带宽不达标；恢复至 85% 以上自动解除；
6. 停中心 10 分钟再启 → 探针本地缓存，恢复自动补传（日志 `cache flushed`），期间无误报；
7. 后台上传新版 → 对 1 台升级 → 版本号变化 → 再灰度全量（30s 内自更新，失败不影响原版本）；
8. 建 1 小时静默重复④ → 0 新告警；P0P1/P2 不同收件组分级送达验证；
9. 手动日报/周报 + CSV（Excel 无乱码）+ 次日 08:00 自动日报邮件。

**沙箱预演已通过**（2026-10-26）：P1 46s 触发/12s 恢复；静默抑制 100%、删静默 27s 补触发；40% 降质迟滞恢复；12 死探针盲区+删除收口；255 万样本压测（VM 25MB/30 天，总览 API 2-4ms）。剩余现场项：场景 2 真实楼层故障、真实邮件送达、自动日报到时触发。

**验收 14 项核对表**：①实时监测<30s ②P1≤30s 邮件 ③故障域文案区分 ④P0≤60s ⑤断网补传无丢失 ⑥测速可解释/串行不扰办公 ⑦带宽告警 ⑦天无误报 ⑨三类邮件送达 ⑩报表 CSV ⑪权限审计 ⑫自更新闭环 ⑬大屏 48h 投屏 ⑭文档培训完成（记录栏模板见 git 历史 docs/05）。

### 14.6 用户手册 FAQ 精选（原 06）

- 线路"未知"=未绑定探针；收件组分 P0P1/P2，留空用默认收件人；
- 测速值与运营商宣传带宽有偏差属正常（对端节点/时段负载），以线路详情 7 天趋势为准；
- 可只测速不告警（关测速计划，连通性告警独立）；
- 重要楼层推荐双探针交叉验证（两台都报中断才 P1，单台报→P2 冲突提示）；
- 真实流量占用（非测速）看 SNMP 采集曲线；大屏 F11 全屏投屏、关闭显示器休眠。

### 14.7 调研/设计/开发计划结论（原 01/02/03）

- **调研**：无现成整体方案（Zabbix/PRTG 过重、Uptime Kuma 无分布式探针）→ 自研 + 复用 Uptime Kuma 前端栈（MIT 协议合规已核实）；
- **设计默认值**：探测间隔 ping 10s/tcp 30s/dns 60s/http 60s/超时 3s；目标=3 公共 DNS(223.5.5.5/119.29.29.29/114.114.114.114) + qq/baidu:443 + qq generate_204/baidu HTTP，多目标投票；告警默认 blind 60s、中断窗口 30s、降质丢包 10%/窗口 5min、冷却 300s（全部后台可调）；
- **开发计划**：M0~M4 全部完成（2026-10-26）；风险对策与二期候选清单见 git 历史 docs/03。
