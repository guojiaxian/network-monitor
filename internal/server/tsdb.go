package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"netwatch/internal/proto"
)

// TSDB 时序写入：批量 POST 到 VictoriaMetrics /api/v1/import/prometheus（文本格式）。
type TSDB struct {
	baseURL string
	hc      *http.Client
}

func NewTSDB(baseURL string) *TSDB {
	return &TSDB{baseURL: baseURL, hc: &http.Client{Timeout: 10 * time.Second}}
}

// tsPoint 一条时序样本
type tsPoint struct {
	metric string
	labels [][2]string
	value  float64
	tsMs   int64
}

func (t *tsPoint) render(b *bytes.Buffer) {
	b.WriteString(t.metric)
	b.WriteByte('{')
	for i, kv := range t.labels {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[0])
		b.WriteString(`="`)
		b.WriteString(escapeLabel(kv[1]))
		b.WriteByte('"')
	}
	b.WriteString("} ")
	b.WriteString(strconv.FormatFloat(t.value, 'f', -1, 64))
	b.WriteByte(' ')
	b.WriteString(strconv.FormatInt(t.tsMs, 10))
	b.WriteByte('\n')
}

func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// WritePoints 直接写入一组已构造的指标点（SNMP 等内部采集用）。
func (t *TSDB) WritePoints(ctx context.Context, points []tsPoint) error {
	if len(points) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for i := range points {
		points[i].render(&buf)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.baseURL+"/api/v1/import/prometheus", bytes.NewReader(buf.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("vm import: %s", resp.Status)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// WriteBatch 将探针样本转为指标并写入。lineName 用于按线路维度打标签。
func (t *TSDB) WriteBatch(ctx context.Context, agentID, lineName string, samples []proto.Sample) error {
	if len(samples) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, s := range samples {
		base := [][2]string{{"probe", agentID}}
		if lineName != "" {
			base = append(base, [2]string{"line", lineName})
		}
		target := [2]string{"target", s.Target}
		switch s.Type {
		case proto.TypePing:
			m := "nw_ping_rtt_ms"
			if s.Extra["gw"] == 1 {
				base = append(base, [2]string{"class", "gw"})
			} else {
				base = append(base, [2]string{"class", "public"})
			}
			if s.OK {
				buf_write(&buf, m, append(base, target), s.RTTms, s.TS)
				buf_write(&buf, "nw_ping_ok", append(base, target), 1, s.TS)
			} else {
				buf_write(&buf, "nw_ping_ok", append(base, target), 0, s.TS)
			}
			buf_write(&buf, "nw_ping_loss_pct", append(base, target), s.LossPct, s.TS)
		case proto.TypeTCP:
			if s.OK {
				buf_write(&buf, "nw_tcp_rtt_ms", append(base, target), s.RTTms, s.TS)
				buf_write(&buf, "nw_tcp_ok", append(base, target), 1, s.TS)
			} else {
				buf_write(&buf, "nw_tcp_ok", append(base, target), 0, s.TS)
			}
		case proto.TypeDNS:
			if s.OK {
				buf_write(&buf, "nw_dns_rtt_ms", append(base, target), s.RTTms, s.TS)
				buf_write(&buf, "nw_dns_ok", append(base, target), 1, s.TS)
			} else {
				buf_write(&buf, "nw_dns_ok", append(base, target), 0, s.TS)
			}
		case proto.TypeHTTP:
			if s.OK {
				buf_write(&buf, "nw_http_ttfb_ms", append(base, target), s.RTTms, s.TS)
				buf_write(&buf, "nw_http_ok", append(base, target), 1, s.TS)
				if code, ok := s.Extra["code"]; ok {
					buf_write(&buf, "nw_http_code", append(base, target), code, s.TS)
				}
				if days, ok := s.Extra["cert_days"]; ok && days > 0 {
					buf_write(&buf, "nw_http_cert_days", append(base, target), days, s.TS)
				}
			} else {
				buf_write(&buf, "nw_http_ok", append(base, target), 0, s.TS)
			}
		case proto.TypeSpeed:
			if v, ok := s.Extra["down_mbps"]; ok {
				buf_write(&buf, "nw_speed_down_mbps", append(base, target), v, s.TS)
			}
			if v, ok := s.Extra["up_mbps"]; ok {
				buf_write(&buf, "nw_speed_up_mbps", append(base, target), v, s.TS)
			}
		}
	}
	if buf.Len() == 0 {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.baseURL+"/api/v1/import/prometheus", bytes.NewReader(buf.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("vm import: %s", resp.Status)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// buf_write 小包装避免重复代码
func buf_write(b *bytes.Buffer, metric string, labels [][2]string, v float64, ts int64) {
	p := tsPoint{metric: metric, labels: labels, value: v, tsMs: ts}
	p.render(b)
}

// vmSeries VM 即时查询返回的一条序列
type vmSeries struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"` // [ts, "value"]
}

// Query 执行即时查询，返回 (line标签 → 值)。仅用于报表统计（表达式按 line 聚合）。
func (t *TSDB) QueryByLine(ctx context.Context, query string) (map[string]float64, error) {
	u := t.baseURL + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var parsed struct {
		Status string `json:"status"`
		Data   struct {
			Result []vmSeries `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("vm query parse: %w", err)
	}
	if parsed.Status != "success" {
		return nil, fmt.Errorf("vm query failed: %s", string(body[:min(len(body), 200)]))
	}
	out := map[string]float64{}
	for _, r := range parsed.Data.Result {
		line := r.Metric["line"]
		if line == "" || len(r.Value) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fmt.Sprint(r.Value[1]), 64)
		if err == nil {
			out[line] = v
		}
	}
	return out, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ExportSeries VM /api/v1/export 返回的一条原始序列（values 与 timestamps 平行）
type ExportSeries struct {
	Metric     map[string]string `json:"metric"`
	Values     []float64         `json:"values"`
	Timestamps []int64           `json:"timestamps"`
}

// Export 拉取原始样本（探测历史页数据源；调用方自行限制时间窗）
func (t *TSDB) Export(ctx context.Context, match string, start, end int64) ([]ExportSeries, error) {
	u := fmt.Sprintf("%s/api/v1/export?match[]=%s&start=%d&end=%d",
		t.baseURL, url.QueryEscape(match), start, end)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vm export: %s", resp.Status)
	}
	out := []ExportSeries{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 256*1024), 8<<20)
	for sc.Scan() {
		var s ExportSeries
		if json.Unmarshal(sc.Bytes(), &s) == nil && len(s.Values) > 0 {
			out = append(out, s)
		}
	}
	return out, sc.Err()
}
