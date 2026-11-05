package server

import "testing"

func TestValidateIndustrySettings(t *testing.T) {
	t.Run("合法目标与间隔兜底", func(t *testing.T) {
		st := IndustrySettings{Enabled: true, Items: []IndustryItem{
			{Name: "行业网核心", Target: "10.5.0.1", IntervalS: 0},
			{Name: "备用", Target: "10.5.0.2", IntervalS: 5},
		}}
		if err := ValidateIndustrySettings(&st); err != nil {
			t.Fatalf("应通过: %v", err)
		}
		if st.Items[0].IntervalS != 30 {
			t.Fatalf("间隔 <10 应兜底 30，实际 %d", st.Items[0].IntervalS)
		}
		if st.Items[1].IntervalS != 30 {
			t.Fatalf("间隔 5 应兜底 30，实际 %d", st.Items[1].IntervalS)
		}
	})
	t.Run("非法 IP 拒绝", func(t *testing.T) {
		st := IndustrySettings{Items: []IndustryItem{{Name: "x", Target: "10.5.0.999"}}}
		if err := ValidateIndustrySettings(&st); err == nil {
			t.Fatal("非法 IP 应被拒绝")
		}
		// host:port 或域名均不合法（仅 ping，必须为 IP）
		st = IndustrySettings{Items: []IndustryItem{{Name: "x", Target: "www.example.com"}}}
		if err := ValidateIndustrySettings(&st); err == nil {
			t.Fatal("域名应被拒绝（仅支持 IP）")
		}
	})
	t.Run("空名称/空目标拒绝", func(t *testing.T) {
		st := IndustrySettings{Items: []IndustryItem{{Name: "", Target: "10.5.0.1"}}}
		if err := ValidateIndustrySettings(&st); err == nil {
			t.Fatal("空名称应被拒绝")
		}
		st = IndustrySettings{Items: []IndustryItem{{Name: "x", Target: ""}}}
		if err := ValidateIndustrySettings(&st); err == nil {
			t.Fatal("空目标应被拒绝")
		}
	})
	t.Run("名称去重", func(t *testing.T) {
		st := IndustrySettings{Items: []IndustryItem{
			{Name: "same", Target: "10.5.0.1"},
			{Name: "same", Target: "10.5.0.2"},
		}}
		if err := ValidateIndustrySettings(&st); err == nil {
			t.Fatal("重复名称应被拒绝")
		}
	})
}
