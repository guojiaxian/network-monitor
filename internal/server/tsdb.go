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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// QueryRange VM 范围查询（/api/v1/query_range），start/end 为 Unix 秒，stepSec 为步长秒。
// 报表统计用：表达式内自带子窗口聚合（如 avg_over_time(...[5m])），调用方对返回序列做二次聚合。
func (t *TSDB) QueryRange(ctx context.Context, query string, start, end, stepSec int64) ([]ExportSeries, error) {
	if stepSec < 1 {
		stepSec = 1
	}
	u := fmt.Sprintf("%s/api/v1/query_range?query=%s&start=%d&end=%d&step=%d",
		t.baseURL, url.QueryEscape(query), start, end, stepSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	var parsed struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Values [][]any           `json:"values"` // 每点 [ts, "value"]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("vm query_range parse: %w", err)
	}
	if parsed.Status != "success" {
		return nil, fmt.Errorf("vm query_range failed: %s", string(body[:min(len(body), 200)]))
	}
	out := []ExportSeries{}
	for _, r := range parsed.Data.Result {
		s := ExportSeries{Metric: r.Metric}
		for _, p := range r.Values {
			if len(p) < 2 {
				continue
			}
			ts, ok1 := vmToTS(p[0])
			v, ok2 := vmToFloat(p[1])
			if !ok1 || !ok2 {
				continue
			}
			s.Timestamps = append(s.Timestamps, ts)
			s.Values = append(s.Values, v)
		}
		if len(s.Values) > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

func vmToTS(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	}
	return 0, false
}

func vmToFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
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
	// 单行上限 64MB：31 天窗口的单序列 NDJSON 行约 6MB，8MB 上限会被静默跳过丢数据
	sc.Buffer(make([]byte, 256*1024), 64<<20)
	for sc.Scan() {
		var s ExportSeries
		if json.Unmarshal(sc.Bytes(), &s) == nil && len(s.Values) > 0 {
			out = append(out, s)
		}
	}
	return out, sc.Err()
}

// LabelValues 枚举匹配序列的某标签取值（VM /api/v1/label/<label>/values，
// 探测历史页目标/监控项下拉数据源；match[] 为完整选择器，可为多个）
func (t *TSDB) LabelValues(ctx context.Context, label string, matches []string) ([]string, error) {
	u, err := url.Parse(t.baseURL + "/api/v1/label/" + url.PathEscape(label) + "/values")
	if err != nil {
		return nil, err
	}
	qs := u.Query()
	for _, m := range matches {
		qs.Add("match[]", m)
	}
	u.RawQuery = qs.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vm label values: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var out struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}
