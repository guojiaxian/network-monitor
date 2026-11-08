package server

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

func fptr(v float64) *float64 { return &v }
func iptr(v int) *int         { return &v }

// ---------- 模板解析 ----------

func TestParseRepTemplate(t *testing.T) {
	tpl := `# 报告标题

编制单位：某单位
{{分页}}

## 一、概况

正文一段。

### 子标题

{{表格:总体概况}}

正文二段。
`
	blocks := parseRepTemplate(tpl)
	var types []string
	for _, b := range blocks {
		types = append(types, b.Type)
	}
	want := []string{"title", "cov", "page", "h1", "para", "h2", "table", "para"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("块序列不符: got %v want %v（%+v）", types, want, blocks)
	}
	if blocks[6].Table != "总体概况" {
		t.Fatalf("表格名解析错误: %q", blocks[6].Table)
	}
	// 表格指令兼容中文冒号
	if blocks := parseRepTemplate("{{表格：线路明细}}"); len(blocks) != 1 || blocks[0].Table != "线路明细" {
		t.Fatalf("中文冒号表格指令未识别: %+v", blocks)
	}
	// 封面行按全角冒号拆分：值不得残留冒号的字节碎片（多字节切分坑）
	lab, val, ok := splitCovLine("编制单位：某某单位")
	if !ok || lab != "编制单位" || val != "某某单位" {
		t.Fatalf("全角冒号拆分错误: %q %q %v", lab, val, ok)
	}
	lab, val, ok = splitCovLine("报告周期:2026-11-07")
	if !ok || lab != "报告周期" || val != "2026-11-07" {
		t.Fatalf("半角冒号拆分错误: %q %q %v", lab, val, ok)
	}
	// 未写分页符时普通段落应为 para 而非 cov
	blocks2 := parseRepTemplate("# 标题\n信息行\n\n正文。")
	for _, b := range blocks2 {
		if b.Type == "cov" {
			t.Fatalf("模板无分页符时不应产生封面块: %+v", blocks2)
		}
	}
	// 空行不产生块
	if blocks3 := parseRepTemplate("a\n\n\n\nb"); len(blocks3) != 2 {
		t.Fatalf("空行未忽略: %+v", blocks3)
	}
}

// ---------- 占位符 ----------

func TestRepInline(t *testing.T) {
	d := &reportData{Kind: "week", Start: "2026-11-02 00:00", End: "2026-11-09 00:00", Generated: "2026-11-09 08:30:00",
		Summary: &repSummary{AlertTotal: 3, AlertP1: 1, AvgAvail: fptr(99.9876)}}
	ctx := &repContext{D: d, Org: "某某单位"}
	got := repInline("{{单位名称}}网络运行{{周期类型}}：{{平均可用率}}%/告警{{告警总数}}/{{不存在变量}}", ctx, nil)
	want := "某某单位网络运行周报：99.99%/告警3/{{不存在变量}}"
	if got != want {
		t.Fatalf("行内替换不符: got %q want %q", got, want)
	}
	// 未知变量要记入 unknown
	var unk []string
	repInline("x {{未知变量}} y", ctx, &unk)
	if len(unk) != 1 || unk[0] != "未知变量" {
		t.Fatalf("未知变量未记录: %v", unk)
	}
	// 旧报表（无 Summary）不 panic，数值变量回退 —
	old := &reportData{Kind: "day"}
	if v, ok := repVarValue(&repContext{D: old}, "平均可用率"); !ok || v != "—" {
		t.Fatalf("旧数据兼容失败: %q %v", v, ok)
	}
}

func TestRepPeriodText(t *testing.T) {
	cases := []struct{ kind, start, end, want string }{
		{"day", "2026-11-06 00:00", "2026-11-07 00:00", "2026-11-06"},
		{"week", "2026-11-02 00:00", "2026-11-09 00:00", "2026-11-02 至 2026-11-08"},
		{"month", "2026-10-01 00:00", "2026-11-01 00:00", "2026-10-01 至 2026-10-31"},
	}
	for _, c := range cases {
		if got := repPeriodText(&reportData{Kind: c.kind, Start: c.start, End: c.end}); got != c.want {
			t.Fatalf("repPeriodText(%s): got %q want %q", c.kind, got, c.want)
		}
	}
}

// ---------- 统计周期（自然周期口径） ----------

func TestReportPeriod(t *testing.T) {
	loc := time.Local
	// 日报：2026-11-07 08:00 触发 → 昨日全天
	s, e := reportPeriod("day", time.Date(2026, 11, 7, 8, 0, 0, 0, loc))
	if s.Format("2006-01-02 15:04") != "2026-11-06 00:00" || e.Format("2006-01-02 15:04") != "2026-11-07 00:00" {
		t.Fatalf("日报周期错误: %s ~ %s", s, e)
	}
	// 周报：周一 2026-11-09 触发 → 上周一 11-02 至本周一 11-09
	s, e = reportPeriod("week", time.Date(2026, 11, 9, 8, 30, 0, 0, loc))
	if s.Format("2006-01-02") != "2026-11-02" || e.Format("2006-01-02") != "2026-11-09" {
		t.Fatalf("周报周期错误: %s ~ %s", s, e)
	}
	// 周报：周日触发（11-08 所在周的周一是 11-02，"上一周"即 10-26 ~ 11-01）
	s, e = reportPeriod("week", time.Date(2026, 11, 8, 8, 30, 0, 0, loc))
	if s.Format("2006-01-02") != "2026-10-26" || e.Format("2006-01-02") != "2026-11-02" {
		t.Fatalf("周报（周日触发）周期错误: %s ~ %s", s, e)
	}
	// 月报：2026-11-01 触发 → 10 月整月
	s, e = reportPeriod("month", time.Date(2026, 11, 1, 8, 0, 0, 0, loc))
	if s.Format("2006-01-02") != "2026-10-01" || e.Format("2006-01-02") != "2026-11-01" {
		t.Fatalf("月报周期错误: %s ~ %s", s, e)
	}
	// 月报跨年边界
	s, e = reportPeriod("month", time.Date(2027, 1, 1, 8, 0, 0, 0, loc))
	if s.Format("2006-01-02") != "2026-12-01" || e.Format("2006-01-02") != "2027-01-01" {
		t.Fatalf("月报（跨年）周期错误: %s ~ %s", s, e)
	}
}

// ---------- 汇总与自动文案 ----------

func TestBuildSummary(t *testing.T) {
	d := &reportData{Lines: []reportLine{
		{Name: "线路1", AvailPct: fptr(99.99), AvgRTTms: fptr(5.0), SpeedAvg: fptr(300)},
		{Name: "线路2", AvailPct: fptr(97.50), AvgRTTms: fptr(9.0), SpeedAvg: fptr(200)},
	}, Floors: []reportFloor{{Name: "1楼"}}}
	s := buildSummary(d, map[string]int{"P1": 2, "P2": 1}, nil)
	if s.AlertTotal != 3 || s.AlertP1 != 2 || s.AlertP2 != 1 {
		t.Fatalf("告警计数错误: %+v", s)
	}
	if s.LineTotal != 2 || s.FloorTotal != 1 {
		t.Fatalf("总数错误: %+v", s)
	}
	if math.Abs(*s.AvgAvail-98.745) > 1e-9 {
		t.Fatalf("平均可用率计算错误: %v", *s.AvgAvail)
	}
	if s.WorstLine != "线路2" {
		t.Fatalf("最差线路判定错误: %q", s.WorstLine)
	}
	if !strings.Contains(s.Overall, "P0/P1 级告警 2 次") {
		t.Fatalf("P1 分支总体评价错误: %q", s.Overall)
	}
	// 全正常分支
	d2 := &reportData{Lines: []reportLine{{Name: "线路1", AvailPct: fptr(99.99)}}}
	s2 := buildSummary(d2, nil, nil)
	if !strings.Contains(s2.Overall, "整体运行平稳") {
		t.Fatalf("平稳分支评价错误: %q", s2.Overall)
	}
	// 偏低但无 P1 分支
	d3 := &reportData{Lines: []reportLine{{Name: "线路X", AvailPct: fptr(98.00)}}}
	s3 := buildSummary(d3, map[string]int{"P2": 1}, nil)
	if !strings.Contains(s3.Overall, "可用率偏低") || !strings.Contains(s3.Conclusion, "线路「线路X」") {
		t.Fatalf("偏低分支文案错误: %q / %q", s3.Overall, s3.Conclusion)
	}
	// 样本总数 = 各线路 count_over_time 求和
	s4 := buildSummary(d, nil, map[string]seriesAgg{"线路1": {Sum: 51840, Count: 288}, "线路2": {Sum: 51840, Count: 288}})
	if s4.Samples != 103680 {
		t.Fatalf("样本总数求和错误: %d", s4.Samples)
	}
}

// ---------- 渲染与 docx 结构 ----------

func renderDocxParts(t *testing.T, tmpl string, d *reportData, org string) map[string]string {
	t.Helper()
	data, err := RenderReportDocx(tmpl, d, org)
	if err != nil {
		t.Fatalf("RenderReportDocx: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip 打开失败: %v", err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开 %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		parts[f.Name] = string(b)
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml", "word/footer1.xml", "word/_rels/document.xml.rels"} {
		if _, ok := parts[name]; !ok {
			t.Fatalf("docx 缺少文件 %s", name)
		}
	}
	for _, name := range []string{"word/document.xml", "word/styles.xml", "word/footer1.xml"} {
		var v any
		if err := xml.Unmarshal([]byte(parts[name]), &v); err != nil {
			t.Fatalf("%s 不是合法 XML: %v", name, err)
		}
	}
	return parts
}

func TestRenderReportDocx(t *testing.T) {
	d := &reportData{Kind: "day", Start: "2026-11-06 00:00", End: "2026-11-07 00:00", Generated: "2026-11-07 08:00:00",
		Lines:    []reportLine{{Name: "联通线路1", Floor: "1-3楼、10楼", AvailPct: fptr(99.98), AvgRTTms: fptr(5.2), AvgLossPct: fptr(0.01), SpeedMax: fptr(335), SpeedAvg: fptr(320), AlertCount: 1}},
		Floors:   []reportFloor{{Name: "1楼", CIDR: "10.8.30.0/25", Lines: "联通线路1", OnlineIPs: iptr(42), ScanTotal: 254, ScannedAt: "11-06 16:00"}},
		Summary:  &repSummary{AlertTotal: 1, AlertP2: 1, Samples: 103680, Overall: "整体运行平稳", Conclusion: "结论测试", WorstBrief: "全部线路可用率正常。"},
		Alerts:   []reportAlert{{Time: "11-06 03:44", Level: "P2", Kind: "speed_low", Title: "网速慢", Duration: "5 分 0 秒", Status: "已恢复"}},
		Speeds:   []reportSpeed{{Line: "联通线路1", Probe: "probe-1", Time: "11-06 23:05", Down: fptr(335.5), Contract: 100}},
		Targets:  []reportItem{{Name: "qq.com:443", Type: "TCP", Target: "qq.com:443", AvailPct: fptr(100), AvgRTT: fptr(8.1)}},
		Monitors: []reportItem{{Name: "中心端Web", Type: "HTTP", Target: "http://10.8.14.200", AvailPct: fptr(100), AvgRTT: fptr(30.2)}},
	}
	parts := renderDocxParts(t, DefaultRepTemplate(), d, "测试单位")
	doc, styles, footer := parts["word/document.xml"], parts["word/styles.xml"], parts["word/footer1.xml"]

	// 解压后的明文不得含 U+FFFD（非法 UTF-8/控制字符被 xml.EscapeText 替换的痕迹）
	if strings.Contains(doc, "\ufffd") {
		t.Fatalf("document.xml 含 U+FFFD 乱码（多为按字节切分多字节字符所致）")
	}

	for _, frag := range []string{
		"网络运行日报",       // {{周期类型}}
		"测试单位",         // {{单位名称}}
		"2026-11-06",   // {{报告周期}}
		"联通线路1",        // 线路明细表
		"99.98",        // 可用率
		"网速慢",          // 告警明细表（repKindShort 短名）
		"335.5",        // 测速表
		"qq.com:443",   // 公网目标表
		"中心端Web",       // 内置监控表
		"结论测试",         // {{结论与建议}}
		"103,680",      // 样本数千分位
		"10.8.30.0/25", // 楼层表
	} {
		if !strings.Contains(doc, frag) {
			t.Fatalf("document.xml 缺少关键内容: %q", frag)
		}
	}
	// 封面信息表：编制人自动填写 + 标签列右对齐 + 无边框表居中
	if !strings.Contains(doc, "NetWatch 自动编制") {
		t.Fatalf("编制人未自动填写")
	}
	if !strings.Contains(doc, `<w:jc w:val="center"/>`) { // 封面信息表整表居中
		t.Fatalf("封面信息表未居中")
	}
	if !strings.Contains(doc, `<w:pStyle w:val="TC"/><w:jc w:val="right"/>`) { // 标签列右对齐
		t.Fatalf("封面信息表标签列未右对齐")
	}
	if !strings.Contains(styles, `w:styleId="H1"`) || !strings.Contains(styles, "黑体") || !strings.Contains(styles, "宋体") {
		t.Fatalf("styles.xml 缺少标题样式/中文字体")
	}
	if !strings.Contains(footer, "fldCharType") {
		t.Fatalf("页脚缺页码字段")
	}
	// 空表格渲染占位行（旧数据兼容路径）
	d2 := &reportData{Kind: "month", Start: "2026-10-01 00:00", End: "2026-11-01 00:00"}
	doc2 := renderDocxParts(t, DefaultRepTemplate(), d2, "")["word/document.xml"]
	if !strings.Contains(doc2, "本报告期无数据或该项未启用") {
		t.Fatalf("空表格未渲染占位行")
	}
	// 未知表格名与未知变量保留提示原文
	doc3 := renderDocxParts(t, "# T\n{{表格:不存在的表}}\n{{错别字变量}}", d2, "")["word/document.xml"]
	if !strings.Contains(doc3, "未定义的表格") || !strings.Contains(doc3, "{{错别字变量}}") {
		t.Fatalf("未知表格/变量未按约定保留")
	}
}

func TestDocxTitleSpacing(t *testing.T) {
	docXML := func(blocks []docxBlock) string {
		data, err := BuildDocx(blocks)
		if err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			if f.Name == "word/document.xml" {
				rc, _ := f.Open()
				defer rc.Close()
				b, _ := io.ReadAll(rc)
				return string(b)
			}
		}
		t.Fatal("缺 document.xml")
		return ""
	}
	// 两行标题：首行保留页首留白但收窄段后，末行收窄段前但保留下方留白
	doc := docXML([]docxBlock{{Type: "title", Text: "单位名称"}, {Type: "title", Text: "报告名"}})
	if !strings.Contains(doc, `w:before="1200" w:after="240"`) {
		t.Fatalf("首行标题间距未按预期覆盖")
	}
	if !strings.Contains(doc, `w:before="240" w:after="720"`) {
		t.Fatalf("末行标题间距未按预期覆盖")
	}
	// 单标题：用样式默认，不写显式间距
	doc2 := docXML([]docxBlock{{Type: "title", Text: "单标题"}})
	if strings.Contains(doc2, `w:spacing w:before`) {
		t.Fatalf("单标题不应产生显式间距覆盖")
	}
}

func TestAggSeries(t *testing.T) {
	ss := []ExportSeries{
		{Metric: map[string]string{"line": "线路1"}, Values: []float64{1, 2, 3}, Timestamps: []int64{1, 2, 3}},
		{Metric: map[string]string{"line": "线路2"}, Values: []float64{10}, Timestamps: []int64{9}},
		{Metric: map[string]string{"probe": "p1"}, Values: []float64{99}}, // 无 line 标签 → 跳过
	}
	m := aggSeries(ss, "line")
	if len(m) != 2 {
		t.Fatalf("aggSeries 结果数错误: %d", len(m))
	}
	a := m["线路1"]
	if a.Count != 3 || a.Avg != 2 || a.Max != 3 || a.Min != 1 || a.Sum != 6 {
		t.Fatalf("aggSeries 数学错误: %+v", a)
	}
}
