package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"netwatch/internal/proto"
)

const (
	ReporterFlushSize     = 200             // 实时批量上限
	ReporterFlushInterval = 3 * time.Second // 定时刷盘
	ReporterCacheMaxBytes = 48 << 20        // 断网缓存上限（超过后轮转丢弃最旧文件）
	cacheBatchN           = 500             // 补传时每批条数
)

// reporter 聚合探测结果并批量上报；上报失败写入本地 JSONL 缓存，
// 恢复后自动补传（先旧后新），保证中心失联期间数据不丢。
type reporter struct {
	a      *Agent
	mu     sync.Mutex // 保护缓存文件操作
	lastOK bool
}

func newReporter(a *Agent) *reporter { return &reporter{a: a} }

func (r *reporter) run(ctx context.Context) {
	buf := make([]proto.Sample, 0, ReporterFlushSize+1)
	tick := time.NewTicker(ReporterFlushInterval)
	defer tick.Stop()

	for {
		select {
		case s := <-r.a.sampleCh:
			buf = append(buf, s)
			if len(buf) >= ReporterFlushSize {
				r.flush(ctx, &buf)
			}
		case <-tick.C:
			r.drainCache(ctx) // 先补传旧数据
			if len(buf) > 0 {
				r.flush(ctx, &buf)
			}
		case <-ctx.Done():
			if len(buf) > 0 {
				r.flush(context.Background(), &buf)
			}
			return
		}
	}
}

// flush 上报一批；失败则写入本地缓存。
func (r *reporter) flush(ctx context.Context, buf *[]proto.Sample) {
	if len(*buf) == 0 {
		return
	}
	batch := proto.MetricsBatch{AgentID: r.agentID(), Samples: *buf}
	err := r.a.agentReq(ctx, http.MethodPost, "/api/agent/metrics", &batch, nil)
	if err == nil {
		*buf = (*buf)[:0]
		r.lastOK = true
		return
	}
	if errors.Is(err, errReRegister) {
		r.a.abort(errReRegister)
	}
	r.lastOK = false
	r.a.log.Printf("metrics upload failed, caching %d samples: %v", len(*buf), err)
	r.cacheAppend(*buf)
	*buf = (*buf)[:0]
}

func (r *reporter) agentID() string {
	r.a.identMu.RLock()
	defer r.a.identMu.RUnlock()
	return r.a.ident.AgentID
}

// cacheAppend 追加一批样本到缓存文件；超过上限时轮转丢弃最旧文件。
func (r *reporter) cacheAppend(samples []proto.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.a.cachePath()
	if st, err := os.Stat(p); err == nil && st.Size() > ReporterCacheMaxBytes {
		_ = os.Rename(p, p+".1") // 旧缓存留给人工处理，重新积累
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, s := range samples {
		_ = enc.Encode(&s)
	}
}

// drainCache 链路健康且缓存非空时补传：全量读入 → 逐批上报 → 成功后重写剩余部分。
func (r *reporter) drainCache(ctx context.Context) {
	if !r.lastOK {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	p := r.a.cachePath()
	f, err := os.Open(p)
	if err != nil {
		return // 无缓存
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, string(sc.Bytes()))
	}
	f.Close()
	if len(lines) == 0 {
		_ = os.Remove(p)
		return
	}

	for i := 0; i < len(lines); i += cacheBatchN {
		end := i + cacheBatchN
		if end > len(lines) {
			end = len(lines)
		}
		batch := proto.MetricsBatch{AgentID: r.agentID()}
		for _, ln := range lines[i:end] {
			var s proto.Sample
			if json.Unmarshal([]byte(ln), &s) == nil {
				batch.Samples = append(batch.Samples, s)
			}
		}
		if len(batch.Samples) == 0 {
			continue
		}
		if err := r.a.agentReq(ctx, http.MethodPost, "/api/agent/metrics", &batch, nil); err != nil {
			if errors.Is(err, errReRegister) {
				r.a.abort(errReRegister)
			}
			r.lastOK = false
			// 重写剩余未成功的行
			_ = r.rewriteCache(p, lines[i:])
			return
		}
	}
	_ = os.Remove(p)
	if len(lines) > 0 {
		r.a.log.Printf("cache flushed: %d samples", len(lines))
	}
}

func (r *reporter) rewriteCache(p string, rest []string) error {
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	for _, ln := range rest {
		if !strings.HasSuffix(ln, "\n") {
			ln += "\n"
		}
		if _, err := io.WriteString(f, ln); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
