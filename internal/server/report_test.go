package server

import (
	"strings"
	"testing"
)

// TestReportScheduleTextSplit 报表计划文案区分"生成时刻"与"发送时刻"（发送留空=生成后立即发送）
func TestReportScheduleTextSplit(t *testing.T) {
	def := DefaultReportSettings()

	// 发送时刻留空：兼容旧语义，文案写明"生成后立即发送"
	rs := def
	rs.DailyEnabled, rs.WeeklyEnabled, rs.MonthlyEnabled = true, false, false
	rs.DailyAt, rs.DailySendAt = "08:00", ""
	txt, ok := reportScheduleText(rs, def)
	if !ok || txt != "日报 每日 08:00 生成后立即发送" {
		t.Errorf("send empty: %q ok=%v", txt, ok)
	}

	// 生成/发送分离
	rs.DailySendAt = "09:30"
	if txt, _ = reportScheduleText(rs, def); txt != "日报 每日 08:00 生成、09:30 发送" {
		t.Errorf("send set: %q", txt)
	}

	// 三个报表各自独立
	rs.WeeklyEnabled, rs.WeeklyDay, rs.WeeklyAt, rs.WeeklySendAt = true, 1, "07:00", "08:00"
	rs.MonthlyEnabled, rs.MonthlyDay, rs.MonthlyAt, rs.MonthlySendAt = true, 1, "06:00", ""
	txt, _ = reportScheduleText(rs, def)
	for _, want := range []string{"日报 每日 08:00 生成、09:30 发送", "周报 每周周一 07:00 生成、08:00 发送", "月报 每月 1 日 06:00 生成后立即发送"} {
		if !strings.Contains(txt, want) {
			t.Errorf("combined text missing %q: %q", want, txt)
		}
	}

	// 全关
	rs.DailyEnabled, rs.WeeklyEnabled, rs.MonthlyEnabled = false, false, false
	if txt, ok = reportScheduleText(rs, def); ok || txt != "未启用任何报表" {
		t.Errorf("disabled: %q ok=%v", txt, ok)
	}
}

// TestDefaultReportSettingsSendEmpty 默认发送时刻为空 = 生成后立即发送（升级不改变现有行为）
func TestDefaultReportSettingsSendEmpty(t *testing.T) {
	d := DefaultReportSettings()
	if d.DailySendAt != "" || d.WeeklySendAt != "" || d.MonthlySendAt != "" {
		t.Errorf("default send-at should be empty: %q %q %q", d.DailySendAt, d.WeeklySendAt, d.MonthlySendAt)
	}
}
