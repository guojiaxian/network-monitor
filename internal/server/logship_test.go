package server

import (
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"
)

// ---- 格式构建器 ----

// TestCEFBody CEF 载荷：签名 ID=kind、severity 数值、msg 标准扩展、cs 槽位按 cefSlots 固定映射、空值跳过、值转义。
func TestCEFBody(t *testing.T) {
	evt := LogEvent{
		Type: "alert", Kind: "line_down", Severity: "P1", Status: "firing",
		Title: `联通线路2：线|路=\中断`, Msg: "公网全部目标失败",
		Fields: [][2]string{
			{"line", "联通线路2"}, {"floor", "4楼"}, {"cidr", ""}, // cidr 空 → 跳过
			{"probe", "probe-2"}, {"public_ip", "1.2.3.4"},
			{"custom_x", "cx"}, // 槽位表外的字段按原 key 输出
		},
	}
	got := buildCEFBody("0.2", 7, evt) // P1 → CEF severity 7
	want := "CEF:0|NetWatch|NetWatch|0.2|line_down|联通线路2：线\\|路\\=\\\\中断|7|" +
		"msg=公网全部目标失败 " +
		"cs1Label=status cs1=firing " +
		"cs2Label=line cs2=联通线路2 " +
		"cs3Label=floor cs3=4楼 " +
		"cs4Label=probe cs4=probe-2 " +
		"cs6Label=public_ip cs6=1.2.3.4 " +
		"custom_x=cx"
	if got != want {
		t.Fatalf("CEF 载荷不符\n got: %s\nwant: %s", got, want)
	}
}

// TestKVBody KV 正文：event=kind、title 前置、status/severity、字段居中、msg 恒最后、空白转下划线。
func TestKVBody(t *testing.T) {
	evt := LogEvent{
		Type: "speed", Time: time.Unix(1762494600, 0), Title: "线路测速完成",
		Msg: "均值 335.0 Mbps",
		Fields: [][2]string{
			{"probe", "probe-1"}, {"line", "联通线路1"}, {"down_mbps", "335.0"}, {"verdict", "ok"},
		},
	}
	got := buildKVBody(evt)
	want := "event=speed title=线路测速完成 probe=probe-1 line=联通线路1 down_mbps=335.0 verdict=ok msg=均值_335.0_Mbps"
	if got != want {
		t.Fatalf("KV 正文不符\n got: %s\nwant: %s", got, want)
	}
}

// TestBuildSyslogMessage 两种格式的完整报文：CEF=RFC5424 头、KV=RFC3164 头；PRI=112+severity。
func TestBuildSyslogMessage(t *testing.T) {
	evt := alertLogEvent(AlertMeta{
		Kind: "floor_down", Severity: "P1", Title: "5楼：网关不可达",
		Floor: "5楼", ProbeDisplay: "probe-3",
	}, true)
	ts := time.Date(2026, 11, 7, 15, 30, 0, 0, time.FixedZone("CST", 8*3600))
	evt.Time = ts

	st := DefaultSyslogSettings() // cef
	msg := buildSyslogMessage("v1", "nw-host", st, evt)
	// P1 → PRI=112+3=115；RFC5424：`<115>1 <ts> nw-host NetWatch - - - CEF:0...`
	if !strings.HasPrefix(msg, "<115>1 2026-11-07T15:30:00.000+08:00 nw-host NetWatch - - - CEF:0|NetWatch|NetWatch|v1|floor_down|5楼：网关不可达|7|") {
		t.Fatalf("CEF 完整报文头不符: %s", msg)
	}
	if !strings.Contains(msg, "cs1Label=status cs1=firing") {
		t.Fatalf("CEF 报文缺 status 槽位: %s", msg)
	}

	st.Format = "kv"
	msg = buildSyslogMessage("v1", "nw-host", st, evt)
	if !strings.HasPrefix(msg, "<115>Nov  7 15:30:00 nw-host NetWatch: event=floor_down title=5楼：网关不可达 status=firing severity=P1") {
		t.Fatalf("KV 完整报文头不符: %s", msg)
	}
}

// TestSeverityFor P0/P1/P2/其他 → (PRI severity 部分, CEF 0~10)。
func TestSeverityFor(t *testing.T) {
	cases := []struct {
		sev         string
		pri, cefSev int
	}{
		{"P0", 114, 10}, {"P1", 115, 7}, {"P2", 116, 4}, {"", 117, 5}, {"P9", 117, 5},
	}
	for _, c := range cases {
		pri, cs := severityFor(c.sev)
		if pri != c.pri || cs != c.cefSev {
			t.Fatalf("severityFor(%q) = (%d,%d), want (%d,%d)", c.sev, pri, cs, c.pri, c.cefSev)
		}
	}
}

// TestSyslogSettingsNormalize 越界/缺省兜底。
func TestSyslogSettingsNormalize(t *testing.T) {
	st := SyslogSettings{Port: 0, Proto: "sctp", Format: "json", AppName: " ", TimeoutS: 0}
	st.normalize()
	if st.Port != 514 || st.Proto != "udp" || st.Format != "cef" || st.AppName != "NetWatch" || st.TimeoutS != 5 {
		t.Fatalf("normalize 兜底不符: %+v", st)
	}
	st = SyslogSettings{Port: 9514, Proto: "tcp", Format: "kv", AppName: "NW", TimeoutS: 9}
	st.normalize()
	if st.Port != 9514 || st.Proto != "tcp" || st.Format != "kv" || st.AppName != "NW" || st.TimeoutS != 9 {
		t.Fatalf("normalize 覆盖合法值: %+v", st)
	}
	st = SyslogSettings{TimeoutS: 16}
	st.normalize()
	if st.TimeoutS != 5 {
		t.Fatalf("超时越界应回落 5: %d", st.TimeoutS)
	}
}

// ---- Ship 端到端（回环收包）----

func testShipper(st SyslogSettings) *LogShipper {
	return &LogShipper{
		log:  log.New(io.Discard, "", 0),
		host: "nw-host", ver: "test",
		settingsFn: func() SyslogSettings { return st },
	}
}

// TestShipUDP UDP 回环：Ship 异步发出，断言收到的完整 CEF 报文。
func TestShipUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port

	st := DefaultSyslogSettings()
	st.Enabled, st.Host, st.Port = true, "127.0.0.1", port
	testShipper(st).Ship(alertLogEvent(AlertMeta{
		Kind: "line_down", Severity: "P1", Title: "联通线路2：线路中断",
		LineName: "联通线路2", Floor: "4楼", ProbeDisplay: "probe-2", PublicIP: "1.2.3.4",
	}, true))

	buf := make([]byte, 4096)
	_ = pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("UDP 未收到日志: %v", err)
	}
	got := string(buf[:n])
	for _, want := range []string{"<115>1 ", " nw-host NetWatch - - - CEF:0", "|line_down|联通线路2：线路中断|7|", "cs2Label=line cs2=联通线路2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("UDP 报文缺 %q: %s", want, got)
		}
	}
}

// TestShipTCP TCP 回环：报文以换行分帧完整到达。
func TestShipTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if n, err := conn.Read(buf); err == nil {
			got <- string(buf[:n])
		}
	}()

	st := DefaultSyslogSettings()
	st.Enabled, st.Host, st.Port, st.Proto, st.Format = true, "127.0.0.1", port, "tcp", "kv"
	testShipper(st).Ship(speedLogEvent("probe-1", "联通线路1", 335.0, 55.0, 3, 10, "ok"))

	select {
	case msg := <-got:
		if !strings.HasPrefix(msg, "<117>") || !strings.Contains(msg, "event=speed") ||
			!strings.Contains(msg, "down_mbps=335") || !strings.HasSuffix(msg, "\n") {
			t.Fatalf("TCP 报文不符: %q", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TCP 未收到日志")
	}
}

// TestShipFilters 开关过滤：类型开关关 / 总开关关 / 未知类型 → 均不发送。
func TestShipFilters(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	expectSilent := func(name string, st SyslogSettings, evt LogEvent) {
		t.Helper()
		testShipper(st).Ship(evt)
		buf := make([]byte, 2048)
		_ = pc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if n, _, err := pc.ReadFrom(buf); err == nil {
			t.Fatalf("%s 不应外发，却收到: %s", name, string(buf[:n]))
		}
	}

	// send_alerts=false：告警不发
	st := DefaultSyslogSettings()
	st.Enabled, st.Host, st.Port, st.SendAlerts = true, "127.0.0.1", port, false
	expectSilent("告警开关关", st, LogEvent{Type: "alert", Time: time.Now()})

	// 总开关关：任何事件不发
	st = DefaultSyslogSettings()
	st.Enabled, st.Host, st.Port = false, "127.0.0.1", port
	expectSilent("总开关关", st, LogEvent{Type: "alert", Time: time.Now()})
	expectSilent("未配地址", SyslogSettings{Enabled: true, Port: port}, LogEvent{Type: "alert", Time: time.Now()})

	// 未知类型不发
	st = DefaultSyslogSettings()
	st.Enabled, st.Host, st.Port = true, "127.0.0.1", port
	expectSilent("未知类型", st, LogEvent{Type: "bogus", Time: time.Now()})
}
