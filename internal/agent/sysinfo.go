package agent

import (
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
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

// ---------- 资产采集（注册时一次）与自健康（心跳周期） ----------

// collectSysInfo 采集探针资产信息：CPU/内存/磁盘/内核/发行版/虚拟化/网卡。
// 全部读 /proc 与 /sys，无外部命令依赖；单项失败留空。stateDir 用于磁盘统计。
func collectSysInfo(stateDir string) map[string]string {
	si := map[string]string{}
	si["cpu_model"] = firstLineField("/proc/cpuinfo", "model name")
	si["cpu_cores"] = strconv.Itoa(runtime.NumCPU())
	totalKB, _ := memTotalAvail()
	if totalKB > 0 {
		si["mem_total_mb"] = strconv.Itoa(int(totalKB / 1024))
	}
	if st, err := statfs(stateDir); err == nil {
		si["disk_total_gb"] = strconv.Itoa(int(st.blocks * uint64(st.bsize) / (1 << 30)))
		si["disk_used_gb"] = strconv.Itoa(int((st.blocks - st.bfree) * uint64(st.bsize) / (1 << 30)))
	}
	si["kernel"] = kernelRelease()
	si["distro"] = distroName()
	si["virt"] = detectVirt()
	iface, mac, mtu := primaryNIC()
	si["iface"], si["mac"], si["mtu"] = iface, mac, strconv.Itoa(mtu)
	for k, v := range si {
		if v == "" {
			delete(si, k)
		}
	}
	return si
}

func firstLineField(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, key); i >= 0 {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func memTotalAvail() (totalKB, availKB float64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseFloat(f[1], 64)
		switch f[0] {
		case "MemTotal:":
			totalKB = v
		case "MemAvailable:":
			availKB = v
		}
	}
	return
}

// diskStatfs 文件系统统计（stdlib syscall.Statfs 包装）
type diskStatfs struct {
	blocks, bfree, bavail uint64
	bsize                 int64
}

func statfs(path string) (diskStatfs, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return diskStatfs{}, err
	}
	return diskStatfs{blocks: st.Blocks, bfree: st.Bfree, bavail: st.Bavail, bsize: int64(st.Bsize)}, nil
}

func kernelRelease() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func distroName() string {
	if b, err := os.ReadFile("/etc/kylin-release"); err == nil {
		return strings.TrimSpace(string(b))
	}
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return ""
}

// detectVirt 虚拟化类型：KVM/VMware/Hyper-V/物理机（读 /sys/hypervisor 与 DMI）
func detectVirt() string {
	if b, err := os.ReadFile("/sys/hypervisor/type"); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)) // xen
	}
	for _, f := range []string{"/sys/class/dmi/id/sys_vendor", "/sys/devices/virtual/dmi/id/sys_vendor"} {
		if b, err := os.ReadFile(f); err == nil {
			v := strings.ToLower(strings.TrimSpace(string(b)))
			switch {
			case strings.Contains(v, "qemu"):
				return "kvm"
			case strings.Contains(v, "vmware"):
				return "vmware"
			case strings.Contains(v, "microsoft"):
				return "hyper-v"
			case strings.Contains(v, "huawei"):
				return "" // 华为整机多为物理机，不据此判定
			case strings.Contains(v, "dell") || strings.Contains(v, "lenovo") || strings.Contains(v, "hpe") || strings.Contains(v, "inspur") || strings.Contains(v, "sugon"):
				return "physical"
			}
		}
	}
	return ""
}

// primaryNIC 取默认路由所在网卡（若无则第一个非 lo 网卡）的名称/MAC/MTU
func primaryNIC() (name, mac string, mtu int) {
	iface := detectSourceIface()
	if iface == "" {
		entries, err := os.ReadDir("/sys/class/net")
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.Name() != "lo" {
				iface = e.Name()
				break
			}
		}
	}
	if iface == "" {
		return
	}
	name = iface
	if b, err := os.ReadFile("/sys/class/net/" + iface + "/address"); err == nil {
		mac = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/sys/class/net/" + iface + "/mtu"); err == nil {
		mtu, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	return
}

// detectSourceIface 通过默认路由 /proc/net/route 找出口网卡名
func detectSourceIface() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 3 && f[1] == "00000000" && f[7] == "00000000" {
			return f[0]
		}
	}
	return ""
}

// readSelfHealth 探针自健康：CPU%（相对上次快照的差分）、内存%、磁盘%。
// cpuPrev 为上次心跳的 CPU 累计节拍；首跳 CPU 返回 0（下一跳起有效）。
func readSelfHealth(cpuPrev *[2]uint64) (cpuPct, memPct, diskPct float64) {
	if b, err := os.ReadFile("/proc/stat"); err == nil && strings.HasPrefix(string(b), "cpu ") {
		f := strings.Fields(string(b))[1:]
		var idle, total uint64
		for i, v := range f {
			n, _ := strconv.ParseUint(v, 10, 64)
			total += n
			if i == 3 || i == 4 { // idle + iowait
				idle += n
			}
		}
		if cpuPrev[1] > 0 && total > cpuPrev[1] {
			dTotal, dIdle := total-cpuPrev[1], idle-cpuPrev[0]
			if dTotal > 0 {
				cpuPct = float64(dTotal-dIdle) * 100 / float64(dTotal)
			}
		}
		cpuPrev[0], cpuPrev[1] = idle, total
	}
	totalKB, availKB := memTotalAvail()
	if totalKB > 0 {
		memPct = (totalKB - availKB) * 100 / totalKB
	}
	if st, err := statfs("/var/lib/netwatch-agent"); err == nil && st.blocks > 0 {
		diskPct = float64(st.blocks-st.bfree) * 100 / float64(st.blocks)
	}
	return
}
