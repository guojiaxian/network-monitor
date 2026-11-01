# AGENTS.md — NetWatch 项目 AI 交接记忆

> 本文件是给 AI agent 的项目记忆。任何新的 AI agent 接手本项目前**必须完整阅读本文件**。
> 人类读者的文档在 `docs/`（01-06），本文件包含文档之外的会话级记忆：决议史、环境细节、踩坑记录。

---

## 0. 项目一句话

为单位（12 条联通宽带 × 12 个楼层子网，防火墙按源网段策略路由）自研的出口网络监控系统 **NetWatch**：楼层探针监测每条线路连通性/网速 + 阿里云外网探针监测官网可达性 + 告警（邮件/钉钉/企微）+ Web 管理后台 + 大屏。Go 单模块（agent + server）+ Vue3 前端 + PostgreSQL + VictoriaMetrics。

## 1. 当前状态（截至 2026-10-26）

**开发全部完成**，等待现场实施。里程碑：M0 骨架 / M1 连通性+后台 / M2 测速+大屏 / M3 报表权限审计自更新 / M4 文档+演练预演 —— 全部 ✅。
额外完成的二期功能：IM 告警通道（钉钉/企微）、双探针交叉验证、压测回放（cmd/loadgen）、SNMP 真实流量采集、探针零接触运维（SSH 凭据后台管理+网页终端）、外网组官网探测（含证书检测）、内→内内置监控（中心自探，Uptime Kuma 模式）、探测历史逐条查询（PRTG 风格，/history 页）。

**剩余全部是现场实施事项**（无代码待办）：
1. 中心端部署（`deploy/install-server.sh` 一键：环境检查+自动装 PG/VM/chrony+防火墙+自验证；交付包已自包含 VM 二进制，现场零下载）；
2. 12 台楼层探针 VM（`deploy/install-agent.sh` 单台 / `batch-install-probes.sh` 批量，**先绑定线路再启用**）；install-agent.sh 已在真实麒麟主机全流程实测通过；
3. 阿里云服务器装探针切外网组（中心可达三方案见运维手册 §3.1：公网映射/VPN/FRP）；
4. 真实 SMTP/钉钉/企微/SNMP 联调；
5. 按 `docs/05` 完成 9 项现场演练 + 7×24 试运行 + 验收签字。

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
web/              Vue3 前端（src/views: Setup/Login/Dashboard/Lines/LineDetail/Probes/
                  SshTerm(xterm.js)/Alerts/Reports/Settings + components: StatusBadge/HeartbeatBar）
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
cd /root/network-monitor            # 必须先 cd，shell 的 cwd 会漂移（沙箱特性，见 §7）
make build        # agent+server+web（web 用 npmmirror）
make test         # go test ./...
make dist         # 交付包 dist/netwatch-0.2.tar.gz
# 单独：CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/netwatch-server ./cmd/server
#       cd web && npm run build
```

**重要流程约定：改了 server 代码后必须重启演示环境的 server 进程才生效**（改了 n 次忘记重启导致"改了没效果"的误判，见 §7 坑 1）。

## 5. 演示/测试环境（当前运行态）

- **中心端**：0.0.0.0:18003（局域网经 http://10.8.11.4:18003 访问；2026-10-31 由 18003 改端口），env：`NW_DB=postgres://netwatch:netwatch@127.0.0.1:5433/netwatch...`、`NW_VM_URL=http://127.0.0.1:8428`、`NW_ENROLL_TOKEN=test-token`、`NW_WEB_DIR=web/dist`、`NW_DATA_DIR=data`。以后台任务方式启动（输出重定向 /tmp/nw-server.log）。
- **依赖容器**（docker）：`nwtest-pg`（PG16，宿主 5433）、`nwtest-vm`（VictoriaMetrics v1.102.1，宿主 8428）。
- **探针**：agent1 = root 进程，state /tmp/nw-agent，绑"联通1号线"（internal 组，改名"1楼-东配线间"）；agent2 = **uid 1234** 进程（`setpriv --reuid 1234 --regid 1234 --clear-groups --inh-caps=+net_raw --ambient-caps=+net_raw`，state /tmp/nw-agent2 **属主必须 1234**），现为**外网组**（external），探测 baidu HTTPS。
- **账号**：admin/netwatch123（管理员）、viewer1/viewer12345（观察者）。**会话存内存，server 重启后需重新登录**（`curl -c` 存 /tmp/nw-ck 或读 /tmp/nw-tok）。
- **测试辅助**：/tmp/sshmockd 2222 端口（root/Op@2026!x，已含 PTY 假shell）；SNMP 模拟器 /tmp/mock_snmp.py（udp 1161，community public，注入 300/30Mbps）；两者可能已被停掉，重启命令在文件头注释/本文件 §8。

## 6. 数据模型与指标速查

**PG 表**：floors / lines / probes（含 name、grp、ssh_*、pending_version）/ alerts / silences / reports / audit / agent_releases / users / settings。
**settings 键**：agent_config（内网组探测配置）、agent_config_external（外网组）、internal_monitors（内→内内置监控）、smtp、alert_settings、speed_settings、report_settings、notify_channels、snmp_settings、agent.enroll_token。
**VM 指标**：nw_ping_{rtt_ms,ok,loss_pct}{class=gw|public}、nw_tcp_*、nw_dns_*、nw_http_{ttfb_ms,ok,code,cert_days}、nw_speed_{down,up}_mbps、nw_snmp_{in,out}_bps{line}、nw_monitor_{ok,rtt_ms}{monitor,type}（内→内）。标签：probe/line/target/monitor。
**探针名称**：RegisterReq.Name（install-agent.sh 第 4 参数/-name 传入），注册时落 probes.name；告警邮件/探针列表/未绑定区均显示名称。**注意**：外网组探针不计入 unbound 警告（overviewData 已过滤）。

## 7. 踩坑记录（新 agent 必读，避免重复排查）

1. **改 server 代码后忘记重启进程**→表现为"新功能 404/行为不变"。先 `ss -tlnp | grep 18003` 确认进程启动时间。
2. **Bash 工具的 shell cwd 会漂移**：相对路径 `./bin/...` 偶发 "command not found"（exit 127）。**永远先 `cd /root/network-monitor`**。
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
15. **全功能 API 测试**（/tmp/functest.py，47 用例全过，2026-10-31）：修复过 setup 检查顺序（400/403 颠倒）、线路/楼层重名未拒绝（500→400+提示）、CSV 表头按内容误判（含"楼层"的数据行被跳过）、ssh_port=0 未钳位、assign 不存在线路 500、history limit>5000 归零、agent-releases version 路径穿越（../evil 可写任意目录）、删除用户后其会话仍有效（8 类）。**教训：新加接口必须同步加进 functest.py 回归**。安全模式：safeName() 白名单（版本/文件名）、删除用户同步失效会话、admin 会话内存态。

## 8. 常用操作配方

```bash
# 重启中心端（演示）
cd /root/network-monitor && NW_LISTEN=127.0.0.1:18003 NW_DB='postgres://netwatch:netwatch@127.0.0.1:5433/netwatch?sslmode=disable' NW_VM_URL='http://127.0.0.1:8428' NW_ENROLL_TOKEN=test-token NW_WEB_DIR=/root/network-monitor/web/dist NW_DATA_DIR=/root/network-monitor/data ./bin/netwatch-server > /tmp/nw-server.log 2>&1 &
# 重启 agent2（外网组，uid 1234）
NW_SERVER=http://127.0.0.1:18003 NW_ENROLL_TOKEN=test-token NW_STATE_DIR=/tmp/nw-agent2 setpriv --reuid 1234 --regid 1234 --clear-groups --inh-caps=+net_raw --ambient-caps=+net_raw ./bin/netwatch-agent > /tmp/nw-agent2.log 2>&1 &
# 登录取 token
TOK=$(curl -s -c - -X POST http://127.0.0.1:18003/api/admin/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"netwatch123"}' | grep nw_session | awk '{print $NF}')
# 压测回填 / SNMP mock / SSH mock
./bin/loadgen -server http://127.0.0.1:18003 -token test-token -probes 12 -backfill 7
python3 /tmp/mock_snmp.py &            # udp 1161
/tmp/sshmockd 2222 root 'Op@2026!x' &
```

## 9. 告警语义速查（判定逻辑在 internal/server/alert.go evaluate）

| 级别 | kind | 触发 |
|---|---|---|
| P0 | blind | 探针心跳 >60s |
| P1 | line_down | 绑定线路的全部有效探针（在线+网关OK）报公网全断（投票） |
| P1 | floor_down | 网关连续 3 次失败 |
| P1 | site_down | 外网组全部探针 HTTPS 失败（投票） |
| P2 | monitor_down | 内→内内置监控连续 3 次失败（中心自探，恢复即解除；目标删除自动收口） |
| P2 | degraded/probe_conflict/speed_low/site_conflict/cert_expiring | 见代码 |

通用机制：静默窗口（30s 缓存）、恢复通知、迟滞、按级别收件组、IM 恒推 P0/P1。降质=按目标丢包**下中位数**（抗单坏目标）。状态汇总 statusOrder 确定性顺序（degraded/speed_low 同 rank）。

## 10. API 路由速查

- `/api/agent/*`：register、config（按组）、metrics、heartbeat、download/{id}（自更新，鉴权 X-NW-Agent/X-NW-Secret）
- `/api/admin/*`：bootstrap/setup/login、overview（lines+unbound+external_probes+external_status+internal_monitors 三视角）、floors/lines CRUD+CSV import、probes（列表/assign/group/meta/ssh: test|restart-agent|exec|terminal(WS)/import）、alerts、silences、reports(+generate/export.csv)、settings(+smtp/test、notify/test)、users、audit、agent-releases(+upgrade)、speedtest/run、snmp/ifaces、metrics/query_range（VM 代理）、history（PRTG 风格逐条探测记录，type=ping/tcp/dns/http/monitor/speed + probe/target/start/end/limit）
- `/api/screen/overview`（公开只读大屏）
- 角色：admin 全权限；viewer 只读（非 GET 403；WS 终端显式 403）

## 11. 文档索引

`docs/01` 开源调研（选型依据）· `02` 设计与功能规格（架构/探测/测速/告警/UI）· `03` 开发计划（里程碑全部完成）· `04` 运维手册（部署/升级/备份/故障处置/阿里云三方案）· `05` 演练清单（9 场景+沙箱预演记录+验收模板）· `06` 用户操作手册。README 有完整验证记录时间线。

## 12. Git 与发布

- 仓库：`git@github.com:guojiaxian/network-monitor.git`（main 分支），2026-10-31 首推（v0.2 全量，75 文件）。
- .gitignore 覆盖：bin/、dist/（make dist 重建）、data/（**含 ssh-master.key 严禁入库**）、web/dist、web/node_modules。
- agent1 从本轮起由 systemd 管理（/usr/local/bin + /var/lib/netwatch-agent，id b0f93f84...，绑定联通1号线）；沙箱 /tmp 身份目录丢失即自动重注册，旧行手动删。
- 发布流程：改代码 → make test → make dist → git commit/push。

## 13. 给下一个 AI agent 的行为约定

- 代码注释、日志、UI 文案用**中文**；新功能遵循现有模式（设置进 settings 表+白名单、告警进 kindSeverity/kindTitle、前端加卡片区块）。
- 任何改动：`go build ./... && go vet` 通过 → 重建 bin → **重启演示 server** → 用 curl 按 §8 验证 → 更新 README 验证记录。
- 涉及协议（proto/）改动需考虑新旧探针兼容（字段只增不改语义）。
- 用户的新需求先对照 §2 决议史，避免推翻已确认决策；重大方案先问再动。
- 演示环境的测试数据（探针行、告警）可以清理重建，但 **admin 账号、联通1号线、外网组配置**是演示骨架，保持可用。
