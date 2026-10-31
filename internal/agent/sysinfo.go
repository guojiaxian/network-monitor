package agent

import (
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

func goos() string   { return runtime.GOOS }
func goarch() string { return runtime.GOARCH }

// detectSourceIP 返回本机访问互联网时使用的源 IP（不真正发包）。
// 探针位于楼层子网，该 IP 即策略路由匹配的源地址。
func detectSourceIP() string {
	conn, err := net.DialTimeout("udp", "223.5.5.5:53", 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok && !ua.IP.IsUnspecified() {
		return ua.IP.String()
	}
	return ""
}

// startSourceRefresher 周期刷新缓存源 IP（网卡/DHCP 变更后自动跟随）。
func startSourceRefresher(stop <-chan struct{}, out *atomic.Value, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	set := func() {
		if ip := detectSourceIP(); ip != "" {
			out.Store(ip)
		}
	}
	set()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			set()
		}
	}
}

// detectGateway 从 /proc/net/route 读取默认网关（IPv4）。
func detectGateway() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		if f[1] == "00000000" && f[7] == "00000000" { // destination=0.0.0.0，genmask=0.0.0.0
			return parseHexIP(f[2])
		}
	}
	return ""
}

func parseHexIP(hex string) string {
	if len(hex) != 8 {
		return ""
	}
	v, err := strconv.ParseUint(hex, 16, 64)
	if err != nil {
		return ""
	}
	// /proc/net/route 为小端序
	b := [4]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
	return strconv.Itoa(int(b[0])) + "." + strconv.Itoa(int(b[1])) + "." +
		strconv.Itoa(int(b[2])) + "." + strconv.Itoa(int(b[3]))
}

// readMemLoad 返回内存占用 MB 与 1 分钟负载（Linux /proc）。
func readMemLoad() (memUsedMB, load1 float64) {
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, avail float64
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, _ := strconv.ParseFloat(f[1], 64)
			switch f[0] {
			case "MemTotal:":
				total = v / 1024
			case "MemAvailable:":
				avail = v / 1024
			}
		}
		if total > 0 {
			memUsedMB = total - avail
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 1 {
			load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	return
}

// uptimeS 系统运行秒数
func uptimeS() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 1 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}
