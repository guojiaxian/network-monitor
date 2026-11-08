package agent

import (
	"testing"

	"github.com/showwin/speedtest-go/speedtest"

	"netwatch/internal/proto"
)

func speedOKSample(target string, down, up float64) *proto.Sample {
	return &proto.Sample{
		Type: proto.TypeSpeed, Target: target, OK: true,
		Extra: map[string]float64{"down_mbps": down, "up_mbps": up},
	}
}

// TestAverageSample 一轮连测合并为平均样本：全成功/部分成功/全失败
func TestAverageSample(t *testing.T) {
	// 全成功：down/up 取均值，runs/ok_runs 正确
	s := averageSample([]*proto.Sample{
		speedOKSample("联通|节点A", 900, 50),
		speedOKSample("联通|节点A", 940, 70),
		speedOKSample("联通|节点A", 910, 60),
	}, 3)
	if s == nil || !s.OK {
		t.Fatalf("全成功应返回 OK 样本: %+v", s)
	}
	if s.Extra["down_mbps"] != 916.6666666666666 || s.Extra["up_mbps"] != 60 {
		t.Fatalf("均值计算错误: %+v", s.Extra)
	}
	if s.Extra["runs"] != 3 || s.Extra["ok_runs"] != 3 || s.Target != "联通|节点A" {
		t.Fatalf("runs/target 错误: %+v", s)
	}

	// 部分成功（1 次失败 2 次成功）：取成功样本均值
	s = averageSample([]*proto.Sample{
		speedOKSample("t", 100, 10),
		speedFailSample("t", "下载测速失败: boom"),
		speedOKSample("t", 200, 30),
	}, 3)
	if s == nil || !s.OK || s.Extra["down_mbps"] != 150 || s.Extra["ok_runs"] != 2 {
		t.Fatalf("部分成功应取成功样本均值: %+v", s)
	}

	// 全失败：返回 nil（调用方兜底上报失败样本）
	if s := averageSample([]*proto.Sample{
		speedFailSample("t", "e1"), speedFailSample("t", "e2"),
	}, 2); s != nil {
		t.Fatalf("全失败应返回 nil: %+v", s)
	}
}

// TestPickUnicomServer 节点优选：联通节点优先，无联通回退最近节点
func TestPickUnicomServer(t *testing.T) {
	servers := []*speedtest.Server{
		{Sponsor: "China Telecom", Name: "Jinan"},
		{Sponsor: "China Unicom Shandong", Name: "Jinan-5G"},
		{Sponsor: "China Mobile", Name: "Qingdao"},
	}
	if got := pickUnicomServer(servers); got.Sponsor != "China Unicom Shandong" {
		t.Fatalf("应优选联通节点，实际=%s", got.Sponsor)
	}
	servers = []*speedtest.Server{
		{Sponsor: "China Telecom", Name: "Jinan"},
		{Sponsor: "China Mobile", Name: "Qingdao"},
	}
	if got := pickUnicomServer(servers); got.Sponsor != "China Telecom" {
		t.Fatalf("无联通节点应回退最近节点，实际=%s", got.Sponsor)
	}
}
