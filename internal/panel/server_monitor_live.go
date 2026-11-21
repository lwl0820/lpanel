package panel

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type monitorDisk struct {
	Mount      string `json:"mount"`
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
}

type monitorSample struct {
	LoadAverage      *[3]float64 `json:"load_average"`
	cpuIDs           []string
	cpuModel         string
	cpuCoreCount     *int
	At               int64         `json:"at"`
	CPUPercent       *float64      `json:"cpu_percent"`
	MemoryUsedBytes  uint64        `json:"memory_used_bytes"`
	MemoryTotalBytes uint64        `json:"memory_total_bytes"`
	Disks            []monitorDisk `json:"disks"`
}

type monitorSummary struct {
	CPUModel         string       `json:"cpu_model"`
	CPULogicalCount  int          `json:"cpu_logical_count"`
	CPUCoreCount     *int         `json:"cpu_core_count"`
	LoadAverage      *[3]float64  `json:"load_average"`
	At               int64        `json:"at"`
	CPUPercent       *float64     `json:"cpu_percent"`
	MemoryUsedBytes  uint64       `json:"memory_used_bytes"`
	MemoryTotalBytes uint64       `json:"memory_total_bytes"`
	RootDisk         *monitorDisk `json:"root_disk"`
}

type monitorCPUCounter struct {
	busy uint64
	idle uint64
}

type monitorProcess struct {
	PID        int      `json:"pid"`
	Name       string   `json:"name"`
	CPUPercent *float64 `json:"cpu_percent"`
	RSSBytes   uint64   `json:"rss_bytes"`
	ReadBPS    *float64 `json:"read_bps"`
	WriteBPS   *float64 `json:"write_bps"`
}

type monitorProcessCounter struct {
	at         time.Time
	startTicks uint64
	cpuTicks   uint64
	readBytes  *uint64
	writeBytes *uint64
}

type monitorProcesses struct {
	At        int64            `json:"at"`
	Processes []monitorProcess `json:"processes"`
}

type monitorLiveState struct {
	sampleMu       sync.Mutex
	sampleReadAt   time.Time
	sample         monitorSample
	cpu            monitorCPUCounter
	processMu      sync.Mutex
	processReadAt  time.Time
	processes      monitorProcesses
	processCounter map[int]monitorProcessCounter
}

const (
	monitorLiveReuse  = time.Second
	monitorLiveMaxGap = 30 * time.Second
)

func (a *App) registerMonitorLive() {
	const prefix = "GET /api/v1/server/monitor"
	a.Mux.Handle(prefix+"/summary", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sample, err := a.monitorLive.readSample()
		if err != nil {
			WriteError(w, http.StatusServiceUnavailable, "monitor_unavailable", "暂时无法读取服务器状态")
			return
		}
		summary := monitorSummary{
			At: sample.At, CPUPercent: sample.CPUPercent, LoadAverage: sample.LoadAverage,
			CPUModel: sample.cpuModel, CPULogicalCount: len(sample.cpuIDs), CPUCoreCount: sample.cpuCoreCount,
			MemoryUsedBytes: sample.MemoryUsedBytes, MemoryTotalBytes: sample.MemoryTotalBytes,
		}
		for _, disk := range sample.Disks {
			if disk.Mount == "/" {
				summary.RootDisk = new(disk)
				break
			}
		}
		WriteJSON(w, http.StatusOK, summary)
	})))
	a.Mux.Handle(prefix+"/disks", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := time.Now().Unix()
		disks, err := readMonitorDisks()
		if err != nil {
			WriteError(w, http.StatusServiceUnavailable, "monitor_unavailable", "暂时无法读取磁盘状态")
			return
		}
		WriteJSON(w, http.StatusOK, struct {
			At    int64         `json:"at"`
			Disks []monitorDisk `json:"disks"`
		}{At: at, Disks: disks})
	})))
	a.Mux.Handle(prefix+"/processes", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		processes, err := a.monitorLive.readProcesses()
		if err != nil {
			WriteError(w, http.StatusServiceUnavailable, "monitor_unavailable", "暂时无法读取进程状态")
			return
		}
		WriteJSON(w, http.StatusOK, processes)
	})))
}

func (state *monitorLiveState) readSample() (monitorSample, error) {
	state.sampleMu.Lock()
	defer state.sampleMu.Unlock()
	gap := time.Since(state.sampleReadAt)
	if gap < monitorLiveReuse {
		return state.sample, nil
	}
	if gap > monitorLiveMaxGap {
		state.cpu = monitorCPUCounter{}
	}
	sample, err := readMonitorSample(&state.cpu)
	if err != nil {
		return monitorSample{}, err
	}
	sample.cpuModel, sample.cpuCoreCount = state.sample.cpuModel, state.sample.cpuCoreCount
	if state.sampleReadAt.IsZero() || !slices.Equal(sample.cpuIDs, state.sample.cpuIDs) {
		info, _ := os.ReadFile("/proc/cpuinfo")
		sample.cpuModel, sample.cpuCoreCount = parseMonitorCPUInfo(string(info), len(sample.cpuIDs))
		if sample.cpuModel == "" {
			sample.cpuModel = readMonitorCPUModel()
		}
		if sample.cpuCoreCount == nil && len(sample.cpuIDs) > 0 {
			sample.cpuCoreCount = readMonitorCoreCount(sample.cpuIDs)
		}
	}
	state.sample, state.sampleReadAt = sample, time.Now()
	return sample, nil
}

func readMonitorSample(previous *monitorCPUCounter) (monitorSample, error) {
	at := time.Now().Unix()
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return monitorSample{}, err
	}
	current, err := parseMonitorCPU(string(stat))
	if err != nil {
		return monitorSample{}, err
	}
	meminfo, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return monitorSample{}, err
	}
	var total, available *uint64
	for line := range strings.SplitSeq(string(meminfo), "\n") {
		name, value, _ := strings.Cut(line, ":")
		if name != "MemTotal" && name != "MemAvailable" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) != 2 || fields[1] != "kB" {
			return monitorSample{}, fmt.Errorf("无法读取内存统计字段 %s", name)
		}
		kib, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return monitorSample{}, fmt.Errorf("无法读取内存统计字段 %s：%w", name, err)
		}
		if name == "MemTotal" {
			total = new(kib * 1024)
		} else {
			available = new(kib * 1024)
		}
	}
	if total == nil || *total == 0 || available == nil {
		return monitorSample{}, errors.New("内存统计缺少总量或可用量")
	}
	disks, err := readMonitorDisks()
	if err != nil {
		return monitorSample{}, err
	}
	var cpuPercent *float64
	if *previous != (monitorCPUCounter{}) && current.busy >= previous.busy && current.idle >= previous.idle {
		busy, idle := current.busy-previous.busy, current.idle-previous.idle
		if busy+idle > 0 {
			cpuPercent = new(100 * float64(busy) / float64(busy+idle))
		}
	}
	*previous = current
	var cpuIDs []string
	_, perCPU, _ := strings.Cut(string(stat), "\n")
	for line := range strings.SplitSeq(perCPU, "\n") {
		label, _, _ := strings.Cut(line, " ")
		id, ok := strings.CutPrefix(label, "cpu")
		if !ok || id == "" {
			break
		}
		cpuIDs = append(cpuIDs, id)
	}
	load, _ := os.ReadFile("/proc/loadavg")
	return monitorSample{
		cpuIDs: cpuIDs, LoadAverage: parseMonitorLoadAverage(string(load)),
		At: at, CPUPercent: cpuPercent, MemoryTotalBytes: *total,
		MemoryUsedBytes: *total - min(*total, *available), Disks: disks,
	}, nil
}

func parseMonitorLoadAverage(data string) *[3]float64 {
	fields := strings.Fields(data)
	if len(fields) < 3 {
		return nil
	}
	var load [3]float64
	for i := range load {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil
		}
		load[i] = value
	}
	return &load
}

func parseMonitorCPUInfo(data string, logicalCount int) (string, *int) {
	var model, processor string
	cores := make(map[[2]int]struct{})
	known := 0
	for block := range strings.SplitSeq(data, "\n\n") {
		var logicalCPU bool
		socket, core := -1, -1
		for line := range strings.SplitSeq(block, "\n") {
			name, value, _ := strings.Cut(line, ":")
			value = strings.TrimSpace(value)
			switch strings.TrimSpace(name) {
			case "model name":
				model = cmp.Or(model, value)
			case "Processor":
				processor = cmp.Or(processor, value)
			case "processor":
				logicalCPU = true
			case "physical id":
				if id, err := strconv.Atoi(value); err == nil {
					socket = id
				}
			case "core id":
				if id, err := strconv.Atoi(value); err == nil {
					core = id
				}
			}
		}
		if logicalCPU && socket >= 0 && core >= 0 {
			cores[[2]int{socket, core}] = struct{}{}
			known++
		}
	}
	model = cmp.Or(model, processor)
	if known > 0 && known == logicalCount {
		return model, new(len(cores))
	}
	return model, nil
}

func readMonitorCPUModel() string {
	cmd := exec.Command("lscpu")
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		name, value, _ := strings.Cut(line, ":")
		if strings.TrimSpace(name) == "Model name" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func readMonitorCoreCount(cpuIDs []string) *int {
	cores := make(map[string]struct{})
	for _, id := range cpuIDs {
		topology := filepath.Join("/sys/devices/system/cpu", "cpu"+id, "topology")
		// 缺少物理插槽编号时，内核可能使用默认的单线程兄弟列表。
		packageID, err := os.ReadFile(filepath.Join(topology, "physical_package_id"))
		socket, parseErr := strconv.Atoi(strings.TrimSpace(string(packageID)))
		if err != nil || parseErr != nil || socket < 0 {
			return nil
		}
		siblings, err := os.ReadFile(filepath.Join(topology, "core_cpus_list"))
		if err != nil || len(strings.TrimSpace(string(siblings))) == 0 {
			return nil
		}
		cores[string(siblings)] = struct{}{}
	}
	return new(len(cores))
}

func parseMonitorCPU(stat string) (monitorCPUCounter, error) {
	line, _, _ := strings.Cut(stat, "\n")
	fields := strings.Fields(line)
	if len(fields) < 9 || fields[0] != "cpu" {
		return monitorCPUCounter{}, errors.New("缺少整机 CPU 统计")
	}
	var counter monitorCPUCounter
	// guest 和 guest_nice 已计入 user 和 nice，只取前八列。
	for i, field := range fields[1:9] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return monitorCPUCounter{}, fmt.Errorf("无法读取整机 CPU 统计：%w", err)
		}
		if i == 3 || i == 4 {
			counter.idle += value
		} else {
			counter.busy += value
		}
	}
	return counter, nil
}

type monitorMount struct {
	id   uint64
	path string
}

func parseMonitorMounts(data string) []monitorMount {
	mounts := make([]monitorMount, 0)
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for line := range strings.SplitSeq(data, "\n") {
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields := strings.Fields(left)
		if len(fields) < 6 {
			continue
		}
		path := unescape.Replace(fields[4])
		fsType, _, _ := strings.Cut(right, " ")
		switch fsType {
		case "ext2", "ext3", "ext4", "xfs", "btrfs", "f2fs", "bcachefs", "zfs", "jfs", "reiserfs",
			"vfat", "msdos", "exfat", "ntfs", "ntfs3", "fuseblk", "udf", "iso9660", "squashfs", "erofs",
			"nilfs2", "bfs", "hfs", "hfsplus", "minix", "qnx4", "qnx6", "ufs", "ubifs":
		case "overlay":
			if path != "/" {
				continue
			}
		default:
			continue
		}
		id, err := strconv.ParseUint(fields[0], 10, 64)
		if err == nil {
			mounts = append(mounts, monitorMount{id: id, path: path})
		}
	}
	return mounts
}

func readMonitorDisks() ([]monitorDisk, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	disks := make([]monitorDisk, 0)
	seen := make(map[string]bool)
	for _, mount := range parseMonitorMounts(string(data)) {
		if seen[mount.path] {
			continue
		}
		// 按路径查到的挂载 ID 必须与挂载记录一致，以跳过被覆盖的挂载。
		var target unix.Statx_t
		err := unix.Statx(unix.AT_FDCWD, mount.path, unix.AT_NO_AUTOMOUNT|unix.AT_STATX_DONT_SYNC, unix.STATX_MNT_ID|unix.STATX_TYPE, &target)
		if err == nil {
			if target.Mask&unix.STATX_MNT_ID != 0 && target.Mnt_id != mount.id {
				continue
			}
			if target.Mask&unix.STATX_TYPE != 0 && target.Mode&unix.S_IFMT != unix.S_IFDIR {
				continue
			}
		}
		var stat unix.Statfs_t
		if err := unix.Statfs(mount.path, &stat); err != nil || stat.Blocks == 0 {
			continue
		}
		blockSize := uint64(cmp.Or(stat.Frsize, stat.Bsize))
		disks = append(disks, monitorDisk{
			Mount: mount.path, TotalBytes: stat.Blocks * blockSize,
			UsedBytes: (stat.Blocks - min(stat.Blocks, stat.Bfree)) * blockSize,
		})
		seen[mount.path] = true
	}
	slices.SortFunc(disks, func(a, b monitorDisk) int { return cmp.Compare(a.Mount, b.Mount) })
	return disks, nil
}

func (state *monitorLiveState) readProcesses() (monitorProcesses, error) {
	state.processMu.Lock()
	defer state.processMu.Unlock()
	gap := time.Since(state.processReadAt)
	if gap < monitorLiveReuse {
		return state.processes, nil
	}
	if gap > monitorLiveMaxGap {
		state.processCounter = nil
	}

	at := time.Now().Unix()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return monitorProcesses{}, err
	}
	processes := make([]monitorProcess, 0)
	counters := make(map[int]monitorProcessCounter)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() || pid <= 0 {
			continue
		}
		process, current, ok := readMonitorProcess(pid, filepath.Join("/proc", entry.Name()))
		if !ok {
			continue
		}
		if previous, ok := state.processCounter[pid]; ok {
			process.setRates(previous, current)
		}
		processes = append(processes, process)
		counters[pid] = current
	}
	state.processCounter = counters
	state.processes = monitorProcesses{At: at, Processes: processes}
	state.processReadAt = time.Now()
	return state.processes, nil
}

func readMonitorProcess(pid int, path string) (monitorProcess, monitorProcessCounter, bool) {
	// 同一目录描述符读取 stat 和 io，避免 PID 被回收后把另一个进程的计数混入本次采样。
	dir, err := os.OpenRoot(path)
	if err != nil {
		return monitorProcess{}, monitorProcessCounter{}, false
	}
	defer dir.Close()
	stat, err := dir.ReadFile("stat")
	if err != nil {
		return monitorProcess{}, monitorProcessCounter{}, false
	}
	process, counter, ok := parseMonitorProcessStat(pid, string(stat))
	if !ok {
		return monitorProcess{}, monitorProcessCounter{}, false
	}
	counter.at = time.Now()
	if ioStats, err := dir.ReadFile("io"); err == nil {
		for line := range strings.SplitSeq(string(ioStats), "\n") {
			name, raw, _ := strings.Cut(line, ":")
			switch name {
			case "read_bytes", "write_bytes":
				value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
				if err != nil {
					continue
				}
				if name == "read_bytes" {
					counter.readBytes = new(value)
				} else {
					counter.writeBytes = new(value)
				}
			}
		}
	}
	return process, counter, true
}

func parseMonitorProcessStat(pid int, stat string) (monitorProcess, monitorProcessCounter, bool) {
	// 进程名可以包含空格和右括号，从最后一个右括号开始取剩余字段。
	_, rest, open := strings.Cut(stat, " (")
	name, raw, closed := strings.CutLast(rest, ") ")
	fields := strings.Fields(raw)
	if !open || !closed || len(fields) < 22 {
		return monitorProcess{}, monitorProcessCounter{}, false
	}
	// 去掉 PID 和进程名后，以进程状态（第 3 列）为下标 0。
	user, userErr := strconv.ParseUint(fields[11], 10, 64)
	system, systemErr := strconv.ParseUint(fields[12], 10, 64)
	start, startErr := strconv.ParseUint(fields[19], 10, 64)
	rss, rssErr := strconv.ParseInt(fields[21], 10, 64)
	if userErr != nil || systemErr != nil || startErr != nil || rssErr != nil {
		return monitorProcess{}, monitorProcessCounter{}, false
	}
	return monitorProcess{PID: pid, Name: name, RSSBytes: uint64(max(0, rss)) * uint64(os.Getpagesize())},
		monitorProcessCounter{startTicks: start, cpuTicks: user + system}, true
}

func (process *monitorProcess) setRates(previous, current monitorProcessCounter) {
	seconds := current.at.Sub(previous.at).Seconds()
	if seconds <= 0 || current.startTicks != previous.startTicks {
		return
	}
	// 支持的 Linux 架构对用户空间使用每秒 100 个 CPU 时钟计数。
	const ticksPerSecond = 100
	process.CPUPercent = monitorCounterRate(&previous.cpuTicks, &current.cpuTicks, seconds*(ticksPerSecond/100.0))
	process.ReadBPS = monitorCounterRate(previous.readBytes, current.readBytes, seconds)
	process.WriteBPS = monitorCounterRate(previous.writeBytes, current.writeBytes, seconds)
}

func monitorCounterRate(previous, current *uint64, seconds float64) *float64 {
	if previous == nil || current == nil || *current < *previous {
		return nil
	}
	return new(float64(*current-*previous) / seconds)
}
