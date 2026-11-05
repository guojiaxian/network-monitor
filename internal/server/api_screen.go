package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// ScreenAPI 大屏专用只读接口（无需登录，内网使用；仅暴露展示数据，不含任何管理操作）。
type ScreenAPI struct {
	store *Store
	eng   *Engine
	mon   *MonitorRunner
	ind   *IndustryRunner
}

func NewScreenAPI(store *Store, eng *Engine, mon *MonitorRunner, ind *IndustryRunner) *ScreenAPI {
	return &ScreenAPI{store: store, eng: eng, mon: mon, ind: ind}
}

func (a *ScreenAPI) Routes(r chi.Router) {
	r.Get("/overview", a.overview)
}

// overview 返回大屏所需的全部数据：线路状态、当前告警、服务器时间。
func (a *ScreenAPI) overview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	out, err := overviewData(ctx, a.store, a.eng, a.mon, a.ind)
	if err != nil {
		httpError(w, 500, "db")
		return
	}
	alerts, err := a.store.ListAlerts(ctx, "firing", 50)
	if err != nil {
		alerts = []AlertRow{}
	}
	out["alerts"] = alerts
	out["ts"] = time.Now().UnixMilli()
	writeJSON(w, out)
}
