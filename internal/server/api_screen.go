package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ScreenAPI 大屏专用只读接口（无需登录，内网使用；仅暴露展示数据，不含任何管理操作）。
type ScreenAPI struct {
	store *Store
	eng   *Engine
	mon   *MonitorRunner
	ind   *IndustryRunner
	tsdb  *TSDB

	// 今日统计（连通率/平均测速）缓存：大屏每 10s 轮询，VM 聚合查询 60s 内复用一次
	statsMu    sync.Mutex
	statsAt    time.Time
	statsCache screenTodayStats
}

// screenTodayStats 大屏顶部统计条用的当天聚合值（nil = 暂无数据，JSON 输出 null）
type screenTodayStats struct {
	ConnRate *float64 `json:"conn_rate"` // 今日 00:00 至今 ping 样本成功率（0~1）
	AvgSpeed *float64 `json:"avg_speed"` // 今日平均下载 Mbps（剔除专线线路）
}

func NewScreenAPI(store *Store, eng *Engine, mon *MonitorRunner, ind *IndustryRunner, tsdb *TSDB) *ScreenAPI {
	return &ScreenAPI{store: store, eng: eng, mon: mon, ind: ind, tsdb: tsdb}
}

func (a *ScreenAPI) Routes(r chi.Router) {
	r.Get("/overview", a.overview)
}

// todayStats 今日 00:00 至今的连通率与平均测速，口径与总览顶部统计卡一致：
// 连通率 = sum(sum_over_time(nw_ping_ok))/sum(count_over_time(...))（样本加权）；
// 平均测速剔除名称含「专线」的线路（100M 专线不可与 1000M 宽带线路混算）。
// 结果缓存 60s；查询失败返回 nil（前端显示 —），不缓存失败结果。
func (a *ScreenAPI) todayStats(ctx context.Context) screenTodayStats {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	if !a.statsAt.IsZero() && time.Since(a.statsAt) < time.Minute {
		return a.statsCache
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	win := int64(now.Sub(midnight).Seconds())
	if win < 60 {
		win = 60
	}
	end, start := now.Unix(), now.Unix()-60
	q := func(expr string) *float64 {
		ss, err := a.tsdb.QueryRange(ctx, expr, start, end, 60)
		if err != nil || len(ss) == 0 || len(ss[0].Values) == 0 {
			return nil
		}
		v := ss[0].Values[len(ss[0].Values)-1]
		return &v
	}
	out := screenTodayStats{
		ConnRate: q(fmt.Sprintf(`sum(sum_over_time(nw_ping_ok[%ds])) / sum(count_over_time(nw_ping_ok[%ds]))`, win, win)),
		AvgSpeed: q(fmt.Sprintf(`sum(sum_over_time(nw_speed_down_mbps{line!~".*专线.*"}[%ds])) / sum(count_over_time(nw_speed_down_mbps{line!~".*专线.*"}[%ds]))`, win, win)),
	}
	if out.ConnRate != nil || out.AvgSpeed != nil {
		a.statsCache, a.statsAt = out, now
	}
	return out
}

// overview 返回大屏所需的全部数据：线路状态、今日统计、当前告警、服务器时间。
func (a *ScreenAPI) overview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	out, err := overviewData(ctx, a.store, a.eng, a.mon, a.ind)
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	out["today_stats"] = a.todayStats(ctx)
	alerts, _, err := a.store.ListAlerts(ctx, "firing", "", "", time.Time{}, 50, 0)
	if err != nil {
		alerts = []AlertRow{}
	}
	out["alerts"] = alerts
	// 楼层口径（/screen/floor）：按楼层聚合的网络状况（与线路口径同一数据族，均只读展示）
	if floors, err := floorStatusData(ctx, a.store, a.eng); err == nil {
		out["floors"] = floors
	}
	out["ts"] = time.Now().UnixMilli()
	writeJSON(w, out)
}
