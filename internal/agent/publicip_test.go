package agent

import "testing"

// 出口公网 IP 提取：覆盖内置各回显服务的实际响应格式（2026-11-05 现场实测样本）
func TestParseIPv4(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"纯文本(ip.3322.net/3322.org/v4.ident.me)", "218.28.242.106\n", "218.28.242.106"},
		{"ipip 文本", "当前 IP：218.28.242.106  来自于：中国 河南 郑州 联通", "218.28.242.106"},
		{"cip.cc 多行", "IP\t: 218.28.242.106\n地址\t: 中国  河南  郑州\n运营商\t: 联通", "218.28.242.106"},
		{"前后噪声", "your ip is 10.20.30.40, bye", "10.20.30.40"},
		{"非法段拒绝", "999.1.1.1 not an ip", ""},
		{"空响应", "  \n", ""},
		{"无 IP", "internal error", ""},
	}
	for _, c := range cases {
		if got := parseIPv4(c.body); got != c.want {
			t.Errorf("%s: parseIPv4(%q) = %q, want %q", c.name, c.body, got, c.want)
		}
	}
}
