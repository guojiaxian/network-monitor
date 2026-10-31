package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

// SNMP 真实流量采集（二期）：只读轮询防火墙 WAN 口的字节计数器（ifHCIn/OutOctets），
// 差分计算每线真实上下行速率并写入时序库——零测试流量成本，与测速互补。
// 线路 → 接口 的映射由管理员在后台配置（接口列表从设备 walk 获得）。

const (
	oidIfDescr      = "1.3.6.1.2.1.2.2.1.2"      // 接口描述
	oidIfHCInOctets = "1.3.6.1.2.1.31.1.1.1.6"   // 64 位收字节
	oidIfHCOutOctets = "1.3.6.1.2.1.31.1.1.1.10" // 64 位发字节
)

// SnmpSettings SNMP 采集设置
type SnmpSettings struct {
	Enabled   bool             `json:"enabled"`
	Device    string           `json:"device"`    // 防火墙管理 IP
	Community string           `json:"community"` // 只读团体字
	Port      uint16           `json:"port"`
	IntervalS int              `json:"interval_s"` // 轮询间隔（默认 30）
	Mappings  []SnmpMapping    `json:"mappings"`   // 线路 → 接口
}

type SnmpMapping struct {
	LineID  int    `json:"line_id"`
	LineName string `json:"line_name"`
	IfIndex int    `json:"ifindex"`
	IfDescr string `json:"ifdescr"`
}

func DefaultSnmpSettings() SnmpSettings {
	return SnmpSettings{Port: 161, IntervalS: 30}
}

type SnmpIface struct {
	Index  int    `json:"index"`
	Descr  string `json:"descr"`
}

// SnmpPoller SNMP 流量采集器
type SnmpPoller struct {
	store *Store
	tsdb  *TSDB
	log   *log.Logger

	mu       sync.Mutex
	prev     map[int][2]uint64 // ifindex → [in, out] 上次计数
	prevTime time.Time
}

func NewSnmpPoller(store *Store, tsdb *TSDB) *SnmpPoller {
	return &SnmpPoller{store: store, tsdb: tsdb,
		log: log.New(os.Stderr, "snmp ", log.LstdFlags|log.Lmsgprefix), prev: map[int][2]uint64{}}
}

func (s *SnmpPoller) settings(ctx context.Context) SnmpSettings {
	var st SnmpSettings
	if err := s.store.GetSetting(ctx, "snmp_settings", &st); err != nil {
		return DefaultSnmpSettings()
	}
	if st.Port == 0 {
		st.Port = 161
	}
	if st.IntervalS < 10 {
		st.IntervalS = 30
	}
	return st
}

func (s *SnmpPoller) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.poll(ctx)
			}
		}
	}()
}

func snmpConnect(st SnmpSettings) (*gosnmp.GoSNMP, error) {
	g := &gosnmp.GoSNMP{
		Target: st.Device, Port: st.Port, Community: st.Community,
		Version: gosnmp.Version2c, Timeout: 5 * time.Second, Retries: 1,
		MaxOids: 60,
	}
	if err := g.Connect(); err != nil {
		return nil, err
	}
	return g, nil
}

// Ifaces walk 设备接口列表（管理后台选择映射用）
func Ifaces(st SnmpSettings) ([]SnmpIface, error) {
	g, err := snmpConnect(st)
	if err != nil {
		return nil, err
	}
	defer g.Conn.Close()
	out := []SnmpIface{}
	err = g.Walk(oidIfDescr, func(pdu gosnmp.SnmpPDU) error {
		idx := 0
		for i := len(pdu.Name) - 1; i >= 0; i-- {
			if pdu.Name[i] == '.' && i+1 < len(pdu.Name) {
				idx, _ = strconv.Atoi(pdu.Name[i+1:])
				break
			}
		}
		var descr string
		switch v := pdu.Value.(type) {
		case string:
			descr = v
		case []byte:
			descr = string(v)
		}
		out = append(out, SnmpIface{Index: idx, Descr: descr})
		return nil
	})
	return out, err
}

// poll 一轮采集：GET 全部映射接口的收发计数 → 差分 → 入 VM
func (s *SnmpPoller) poll(ctx context.Context) {
	st := s.settings(ctx)
	if !st.Enabled || len(st.Mappings) == 0 || st.Device == "" {
		return
	}
	g, err := snmpConnect(st)
	if err != nil {
		s.log.Printf("connect %s: %v", st.Device, err)
		return
	}
	defer g.Conn.Close()

	var oids []string
	for _, m := range st.Mappings {
		oids = append(oids,
			fmt.Sprintf("%s.%d", oidIfHCInOctets, m.IfIndex),
			fmt.Sprintf("%s.%d", oidIfHCOutOctets, m.IfIndex))
	}
	res, err := g.Get(oids)
	if err != nil {
		s.log.Printf("get: %v", err)
		return
	}
	now := time.Now()
	vals := map[int][2]uint64{} // ifindex → [in,out]
	for _, v := range res.Variables {
		if v.Type == gosnmp.NoSuchObject || v.Type == gosnmp.NoSuchInstance {
			continue
		}
		idx := 0
		in := false
		name := strings.TrimPrefix(v.Name, ".") // gosnmp 返回的 OID 带前导点
		if p, ok := trimPrefix(name, oidIfHCInOctets+"."); ok {
			idx, _ = strconv.Atoi(p)
			in = true
		} else if p, ok := trimPrefix(name, oidIfHCOutOctets+"."); ok {
			idx, _ = strconv.Atoi(p)
		} else {
			continue
		}
		var n uint64
		switch val := v.Value.(type) {
		case uint64:
			n = val
		case uint:
			n = uint64(val)
		case int:
			n = uint64(val)
		default:
			continue
		}
		cur := vals[idx]
		if in {
			cur[0] = n
		} else {
			cur[1] = n
		}
		vals[idx] = cur
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := now.Sub(s.prevTime).Seconds()
	var points []tsPoint
	nowMs := now.UnixMilli()
	for _, m := range st.Mappings {
		cur, ok := vals[m.IfIndex]
		if !ok {
			continue
		}
		prev, had := s.prev[m.IfIndex]
		if had && elapsed > 0 {
			inBps := rateBps(cur[0], prev[0], elapsed)
			outBps := rateBps(cur[1], prev[1], elapsed)
			labels := [][2]string{{"line", m.LineName}}
			points = append(points,
				tsPoint{metric: "nw_snmp_in_bps", labels: labels, value: inBps, tsMs: nowMs},
				tsPoint{metric: "nw_snmp_out_bps", labels: labels, value: outBps, tsMs: nowMs})
			s.log.Printf("line=%s ifindex=%d in=%.1fMbps out=%.1fMbps", m.LineName, m.IfIndex, inBps/1e6, outBps/1e6)
		}
		s.prev[m.IfIndex] = cur
	}
	s.prevTime = now
	if len(points) > 0 {
		if err := s.tsdb.WritePoints(ctx, points); err != nil {
			s.log.Printf("tsdb: %v", err)
		}
	}
}

// rateBps 计数器差分 → 比特每秒（32 位计数器回绕：cur < prev 时按回绕补差；64 位回绕概率极低按 0 处理）
func rateBps(cur, prev uint64, elapsed float64) float64 {
	var d uint64
	if cur >= prev {
		d = cur - prev
	} else {
		d = (1<<32 - prev) + cur
	}
	return float64(d) * 8 / elapsed
}

func trimPrefix(s, prefix string) (string, bool) {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}
