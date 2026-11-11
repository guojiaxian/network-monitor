package server

// reptmpl.go —— 报告模板：解析、占位符渲染、数据表构建与内置默认模板。
//
// 模板为纯文本，管理员在「系统设置→报表」编辑（settings 键 report_templates，按 day/week/month 三份）：
//
//	# 文字         → 封面大标题
//	## 文字        → 一级标题
//	### 文字       → 二级标题
//	{{表格:名称}}   → 整行独占，插入系统预定义数据表（可选名称见 repTableDocs）
//	{{分页}}       → 整行独占，插入分页符
//	其余行         → 正文段落，行内 {{变量}} 替换（变量见 repVarDocs；未知变量原样保留并写日志，便于发现拼写错误）
//
// 封面规则：第一个 {{分页}} 之前的普通段落按封面信息行渲染（居中、不缩进）；模板未写分页符则全部按正文渲染。

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------- 模板解析 ----------

type repBlock struct {
	Type  string // title/h1/h2/cov/para/page/table
	Text  string
	Table string
}

var repTableLineRe = regexp.MustCompile(`^\{\{表格[:：](.+?)\}\}$`)

// parseRepTemplate 行级解析模板文本
func parseRepTemplate(text string) []repBlock {
	var raws []repBlock
	hasPage := false
	for _, raw := range strings.Split(text, "\n") {
		t := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		switch {
		case t == "":
			// 空行仅作编辑视觉分隔，不产生空段
		case strings.HasPrefix(t, "### "):
			raws = append(raws, repBlock{Type: "h2", Text: strings.TrimSpace(t[4:])})
		case strings.HasPrefix(t, "## "):
			raws = append(raws, repBlock{Type: "h1", Text: strings.TrimSpace(t[3:])})
		case strings.HasPrefix(t, "# "):
			raws = append(raws, repBlock{Type: "title", Text: strings.TrimSpace(t[2:])})
		case t == "{{分页}}":
			hasPage = true
			raws = append(raws, repBlock{Type: "page"})
		default:
			if m := repTableLineRe.FindStringSubmatch(t); m != nil {
				raws = append(raws, repBlock{Type: "table", Table: strings.TrimSpace(m[1])})
				continue
			}
			// p0 = 待定段落：有分页符时封面区渲染为 cov，否则按正文
			raws = append(raws, repBlock{Type: "p0", Text: t})
		}
	}
	inCover := true
	out := make([]repBlock, 0, len(raws))
	for _, rb := range raws {
		if rb.Type == "p0" {
			rb.Type = "para"
			if hasPage && inCover {
				rb.Type = "cov"
			}
		}
		if rb.Type == "page" {
			inCover = false
		}
		out = append(out, rb)
	}
	return out
}

// ---------- 占位符 ----------

var repVarRe = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

type repContext struct {
	D   *reportData
	Org string
}

// repInline 行内变量替换；未知变量原样保留并记入 unknown
func repInline(text string, ctx *repContext, unknown *[]string) string {
	return repVarRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := repVarRe.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		name := strings.TrimSpace(sub[1])
		if v, ok := repVarValue(ctx, name); ok {
			return v
		}
		if unknown != nil {
			*unknown = append(*unknown, name)
		}
		return m
	})
}

// repVarValue 占位符取值（中文键）。数值类变量返回纯数字（单位写在模板文字里，防止单位重复）。
func repVarValue(ctx *repContext, name string) (string, bool) {
	d := ctx.D
	var s repSummary
	if d.Summary != nil {
		s = *d.Summary
	}
	switch name {
	case "周期类型":
		switch d.Kind {
		case "week":
			return "周报", true
		case "month":
			return "月报", true
		default:
			return "日报", true
		}
	case "报告周期":
		return repPeriodText(d), true
	case "生成时间":
		return d.Generated, true
	case "编制人":
		return "NetWatch 自动编制", true
	case "单位名称":
		if strings.TrimSpace(ctx.Org) == "" {
			return "____________", true // 留空供打印后手填
		}
		return ctx.Org, true
	case "线路总数":
		return strconv.Itoa(len(d.Lines)), true
	case "楼层总数":
		return strconv.Itoa(len(d.Floors)), true
	case "样本总数":
		return commafy64(s.Samples), true
	case "平均可用率":
		return repNum(s.AvgAvail, 2), true
	case "平均时延":
		return repNum(s.AvgRTT, 1), true
	case "平均丢包率":
		return repNum(s.AvgLoss, 2), true
	case "平均测速":
		return repNum(s.AvgSpeed, 0), true
	case "最差线路名":
		return s.WorstLine, true
	case "异常线路简述":
		return s.WorstBrief, true
	case "告警总数":
		return strconv.Itoa(s.AlertTotal), true
	case "P0告警数":
		return strconv.Itoa(s.AlertP0), true
	case "P1告警数":
		return strconv.Itoa(s.AlertP1), true
	case "P2告警数":
		return strconv.Itoa(s.AlertP2), true
	case "总体评价":
		return s.Overall, true
	case "结论与建议":
		return s.Conclusion, true
	}
	return "", false
}

// repPeriodText 报告周期文案：日报=单日；周报/月报=起始日 至 结束日（period_end 是次日 00:00，减一天为自然周期末日）
func repPeriodText(d *reportData) string {
	s := d.Start
	if len(s) > 10 {
		s = s[:10]
	}
	e := d.End
	if len(e) > 10 {
		e = e[:10]
	}
	switch d.Kind {
	case "day":
		return s
	case "week", "month":
		if ed, err := time.ParseInLocation("2006-01-02", e, time.Local); err == nil {
			return s + " 至 " + ed.AddDate(0, 0, -1).Format("2006-01-02")
		}
	}
	return s + " 至 " + e
}

// repNum 占位符数值格式化：nil 显示 —（区别于表格内 pstr 的空白单元格）
func repNum(v *float64, digits int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.*f", digits, *v)
}

func commafy64(v int64) string {
	s := strconv.FormatInt(v, 10)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return s + "," + strings.Join(parts, ",")
}

// ---------- 数据表构建 ----------

// repKindShort 告警类别短名（报告表格用；完整文案见 alert.go kindTitle）
var repKindShort = map[string]string{
	"line_down":         "线路中断",
	"floor_down":        "楼层中断",
	"blind":             "监控盲区",
	"degraded":          "线路降质",
	"speed_low":         "网速慢",
	"probe_conflict":    "数据冲突",
	"site_down":         "网站不可达",
	"site_conflict":     "外网数据冲突",
	"cert_expiring":     "证书临期",
	"monitor_down":      "内网服务不可达",
	"probe_resource":    "探针资源紧张",
	"industry_down":     "行业网不可达",
	"floor_gw_down":     "楼层网关不可达",
	"floor_anchor_down": "楼层汇聚设备不可达",
}

func repTable(name string, d *reportData) (*docxTable, bool) {
	switch name {
	case "总体概况":
		return repTableSummary(d), true
	case "线路明细":
		return repTableLines(d), true
	case "楼层明细":
		return repTableFloors(d), true
	case "告警明细":
		return repTableAlerts(d), true
	case "测速记录":
		return repTableSpeeds(d), true
	case "公网目标探测":
		return repTableItems(d.Targets, "目标", "TCP/HTTP"), true
	case "内置监控":
		return repTableItems(d.Monitors, "监控项", "ICMP/TCP/HTTP"), true
	case "行业网":
		return repTableItems(d.Industries, "名称", "IP"), true
	case "外网探测":
		return &docxTable{Headers: []string{"项目", "结果"}, Rows: [][]string{
			{"官网 HTTPS 可达性（外网组探针）", "外网组探针未部署，本报告期无外网探测数据"},
		}}, true
	}
	return nil, false
}

func repTableSummary(d *reportData) *docxTable {
	var s repSummary
	if d.Summary != nil {
		s = *d.Summary
	}
	return &docxTable{Headers: []string{"指标", "统计值"}, Rows: [][]string{
		{"监测线路数", fmt.Sprintf("%d 条", len(d.Lines))},
		{"楼层网段数", fmt.Sprintf("%d 个", len(d.Floors))},
		{"平均可用率", pstr(s.AvgAvail, 2) + " %"},
		{"平均时延", pstr(s.AvgRTT, 1) + " ms"},
		{"平均丢包率", pstr(s.AvgLoss, 2) + " %"},
		{"平均测速", pstr(s.AvgSpeed, 0) + " Mbps"},
		{"连通性样本数", commafy64(s.Samples) + " 个"},
		{"告警总数", fmt.Sprintf("%d 次（P0 %d 次 / P1 %d 次 / P2 %d 次）", s.AlertTotal, s.AlertP0, s.AlertP1, s.AlertP2)},
	}}
}

func repTableLines(d *reportData) *docxTable {
	t := &docxTable{Headers: []string{"线路", "关联楼层", "可用率(%)", "平均时延(ms)", "平均丢包(%)", "测速峰值(Mbps)", "测速均值(Mbps)", "告警(次)"}}
	for _, l := range d.Lines {
		t.Rows = append(t.Rows, []string{
			l.Name, l.Floor, pstr(l.AvailPct, 2), pstr(l.AvgRTTms, 1), pstr(l.AvgLossPct, 2),
			pstr(l.SpeedMax, 0), pstr(l.SpeedAvg, 0), strconv.Itoa(l.AlertCount),
		})
	}
	return t
}

func repTableFloors(d *reportData) *docxTable {
	t := &docxTable{Headers: []string{"楼层", "网段", "关联线路", "在线IP(台)", "扫描总数", "最近扫描时间"}}
	for _, f := range d.Floors {
		online := "—"
		if f.OnlineIPs != nil {
			online = strconv.Itoa(*f.OnlineIPs)
		}
		t.Rows = append(t.Rows, []string{f.Name, f.CIDR, f.Lines, online,
			strconv.Itoa(f.ScanTotal), f.ScannedAt})
	}
	return t
}

func repTableAlerts(d *reportData) *docxTable {
	t := &docxTable{Headers: []string{"触发时间", "级别", "类型", "内容", "持续时长", "状态"}}
	for _, a := range d.Alerts {
		kind := repKindShort[a.Kind]
		if kind == "" {
			kind = a.Kind
		}
		t.Rows = append(t.Rows, []string{a.Time, a.Level, kind, a.Title, a.Duration, a.Status})
	}
	if len(d.Alerts) >= repAlertLimit {
		t.Note = fmt.Sprintf("仅显示最近 %d 条告警，完整清单见系统告警中心。", repAlertLimit)
	}
	return t
}

func repTableSpeeds(d *reportData) *docxTable {
	t := &docxTable{Headers: []string{"线路", "探针", "测速时间", "下载(Mbps)", "上传(Mbps)", "契约下行(Mbps)"}}
	for _, s := range d.Speeds {
		contract := "—"
		if s.Contract > 0 {
			contract = strconv.Itoa(s.Contract)
		}
		t.Rows = append(t.Rows, []string{s.Line, s.Probe, s.Time, pstr(s.Down, 1), pstr(s.Up, 1), contract})
	}
	return t
}

// repTableItems 内置监控/行业网/公网目标共用（title 为名称列表头，typeCol 为类型列表头；typeCol 空=不显示类型列）
func repTableItems(items []reportItem, title, typeCol string) *docxTable {
	headers := []string{title}
	if typeCol != "" {
		headers = append(headers, "类型")
	}
	headers = append(headers, "目标地址", "可用率(%)", "平均响应(ms)")
	t := &docxTable{Headers: headers}
	for _, it := range items {
		row := []string{it.Name}
		if typeCol != "" {
			row = append(row, it.Type)
		}
		row = append(row, it.Target, pstr(it.AvailPct, 2), pstr(it.AvgRTT, 1))
		t.Rows = append(t.Rows, row)
	}
	return t
}

// ---------- 渲染入口 ----------

// splitCovLine 封面信息行按第一个全角/半角冒号拆"标签:值"（封面信息表两列对齐用）。
// 注意全角冒号占 3 字节，必须按 rune 宽度切分——s[i+1:] 会把冒号后半截字节留在值里，
// 被 xml.EscapeText 替换成 U+FFFD 乱码。
func splitCovLine(s string) (string, string, bool) {
	if i := strings.Index(s, "："); i > 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len("："):]), true
	}
	if i := strings.Index(s, ":"); i > 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
	}
	return "", "", false
}

// RenderReportDocx 按模板渲染 Word 报告；未知变量/表格名保留占位原文并写日志。
// 封面区（第一个分页符之前）连续的"标签：值"行聚合为无边框信息表，纵向对齐更整齐。
func RenderReportDocx(tmpl string, d *reportData, org string) ([]byte, error) {
	ctx := &repContext{D: d, Org: org}
	var blocks []docxBlock
	var unknown []string
	var covBuf [][2]string
	flushCov := func() {
		if len(covBuf) > 0 {
			blocks = append(blocks, docxBlock{Type: "covtable", Pairs: covBuf})
			covBuf = nil
		}
	}
	for _, rb := range parseRepTemplate(tmpl) {
		switch rb.Type {
		case "cov":
			txt := repInline(rb.Text, ctx, &unknown) // 先替换变量再拆"标签：值"
			if lab, val, ok := splitCovLine(txt); ok {
				covBuf = append(covBuf, [2]string{lab, val})
			} else { // 无冒号的封面行（如口号/说明）flush 表后按居中段落输出
				flushCov()
				blocks = append(blocks, docxBlock{Type: "cov", Text: txt})
			}
		case "table":
			flushCov()
			if t, ok := repTable(rb.Table, d); ok {
				blocks = append(blocks, docxBlock{Type: "table", Table: t})
			} else {
				blocks = append(blocks, docxBlock{Type: "para",
					Text: "〔模板引用了未定义的表格「" + rb.Table + "」，可选表格名称见设置页变量速查〕"})
				unknown = append(unknown, "表格:"+rb.Table)
			}
		default:
			flushCov()
			blocks = append(blocks, docxBlock{Type: rb.Type, Text: repInline(rb.Text, ctx, &unknown)})
		}
	}
	flushCov()
	if len(unknown) > 0 {
		log.Printf("report template: 未识别的占位符（已原样保留）: %v", unknown)
	}
	return BuildDocx(blocks)
}

// ---------- 变量/表格速查（template-default 接口返回给前端展示） ----------

type repDoc struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

var repVarDocs = []repDoc{
	{"周期类型", "日报/周报/月报"},
	{"报告周期", "如 2026-11-06（日报）或 2026-10-01 至 2026-10-31（月报）"},
	{"生成时间", "报表生成时刻"},
	{"编制人", "固定为「NetWatch 自动编制」"},
	{"单位名称", "取「报表推送计划」中的单位名称，未配置时显示填空横线"},
	{"线路总数", "监测的出口线路条数"},
	{"楼层总数", "楼层网段个数"},
	{"样本总数", "报告期内公网连通性（ping）样本总数"},
	{"平均可用率", "全部线路可用率的平均值，纯数字（模板里自行写 %）"},
	{"平均时延", "全部线路平均时延，毫秒，纯数字"},
	{"平均丢包率", "全部线路平均丢包率，纯数字"},
	{"平均测速", "全部线路平均测速，Mbps，纯数字"},
	{"最差线路名", "可用率最低的线路名称"},
	{"异常线路简述", "自动生成的异常线路描述句"},
	{"告警总数", "报告期内告警总次数"},
	{"P0告警数", "P0 级（监控盲区）告警次数"},
	{"P1告警数", "P1 级（线路/楼层/网站中断）告警次数"},
	{"P2告警数", "P2 级告警次数"},
	{"总体评价", "数据驱动的总体评价短句"},
	{"结论与建议", "数据驱动的结论与建议段落"},
}

var repTableDocs = []repDoc{
	{"总体概况", "监测范围与核心指标汇总表"},
	{"线路明细", "逐线路：可用率/时延/丢包/测速峰值均值/告警次数"},
	{"楼层明细", "逐楼层：网段/关联线路/在线 IP/最近扫描"},
	{"告警明细", "报告期内告警清单：时间/级别/类型/内容/持续时长/状态（最近 200 条）"},
	{"测速记录", "逐线路最近一次测速：时间/探针/下行/上行/契约下行"},
	{"公网目标探测", "TCP/HTTP 公网探测目标：可用率/平均响应"},
	{"内置监控", "内→内内置监控项：可用率/平均响应"},
	{"行业网", "行业网监测 IP：可用率/平均响应"},
	{"外网探测", "外网组官网可达性（未部署时显示说明）"},
}

// ---------- 内置默认模板 ----------

const defaultRepTemplate = `# 网络运行{{周期类型}}

监测系统：物探中心网络监控系统（NetWatch）
编制单位：{{单位名称}}
报告周期：{{报告周期}}
编制时间：{{生成时间}}
编制人：{{编制人}}
审核人：____________

{{分页}}

## 一、总体运行概况

本报告期内，物探中心网络监控系统（NetWatch）对本单位楼层网络及互联网出口线路持续监测，共覆盖出口线路 {{线路总数}} 条、楼层网段 {{楼层总数}} 个，采集连通性样本 {{样本总数}} 个。总体评价：{{总体评价}}。

{{表格:总体概况}}

## 二、线路运行明细

本报告期共监测出口线路 {{线路总数}} 条，整体平均可用率 {{平均可用率}}%，平均时延 {{平均时延}} 毫秒，平均丢包率 {{平均丢包率}}%，平均测速 {{平均测速}}Mbps。{{异常线路简述}}

{{表格:线路明细}}

## 三、楼层网络与在线终端

各楼层网段在线终端数以最近一次 IP 扫描结果为准：

{{表格:楼层明细}}

## 四、告警与故障处理

本报告期共触发告警 {{告警总数}} 次，其中 P0 级 {{P0告警数}} 次、P1 级 {{P1告警数}} 次、P2 级 {{P2告警数}} 次。告警明细如下：

{{表格:告警明细}}

## 五、测速记录

各线路最近一次测速结果如下（测速值为探针多连接下载均值）：

{{表格:测速记录}}

## 六、其他监测项

公网目标探测（TCP/HTTP）：

{{表格:公网目标探测}}

内网内置监控：

{{表格:内置监控}}

行业网监测：

{{表格:行业网}}

外网探测：

{{表格:外网探测}}

## 七、结论与建议

{{结论与建议}}
`

// DefaultRepTemplate 内置默认报告模板（三类报告共用，管理员可分别修改覆盖）
func DefaultRepTemplate() string { return defaultRepTemplate }
