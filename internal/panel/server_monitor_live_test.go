package panel

import (
	"net/http"
	"os"
	"slices"
	"testing"
	"time"
)

func TestServerMonitorLiveCPUAndMountParsing(t *testing.T) {
	cpu, err := parseMonitorCPU("cpu 100 10 30 200 20 3 4 5 25 2\ncpu0 1 2 3 4")
	if err != nil || cpu.busy != 152 || cpu.idle != 220 {
		t.Fatalf("整机 CPU 计数错误：%+v，%v", cpu, err)
	}
	mounts := parseMonitorMounts(`20 10 0:42 / / rw - overlay overlay rw
21 20 8:1 / /data\040disk\134dir rw - ext4 /dev/sda1 rw
22 20 0:43 / /proc rw - proc proc rw
23 20 0:44 / /remote rw - nfs4 server:/share rw
24 20 0:45 / /run rw - tmpfs tmpfs rw
25 20 0:46 / /containers/layer rw - overlay overlay rw`)
	want := []monitorMount{{id: 20, path: "/"}, {id: 21, path: `/data disk\dir`}}
	if !slices.Equal(mounts, want) {
		t.Fatalf("本地磁盘挂载点错误：%+v", mounts)
	}
}

func TestServerMonitorLiveProcessRates(t *testing.T) {
	process, previous, ok := parseMonitorProcessStat(42,
		"42 (有空格 ) 的进程) S 1 2 3 4 5 6 7 8 9 10 90 10 0 0 20 0 1 0 132 4444 9")
	if !ok || process.Name != "有空格 ) 的进程" || process.RSSBytes != 9*uint64(os.Getpagesize()) || previous.cpuTicks != 100 || previous.startTicks != 132 {
		t.Fatalf("进程统计解析错误：%+v，%+v", process, previous)
	}
	previous.at = time.Now()
	previous.readBytes = new(uint64(256))
	previous.writeBytes = new(uint64(4096))
	current := monitorProcessCounter{
		at: previous.at.Add(2 * time.Second), startTicks: previous.startTicks,
		cpuTicks: 300, writeBytes: new(uint64(8192)),
	}
	process.setRates(previous, current)
	if process.CPUPercent == nil || *process.CPUPercent != 100 || process.ReadBPS != nil || process.WriteBPS == nil || *process.WriteBPS != 2048 {
		t.Fatalf("CPU 或磁盘速率错误：%+v", process)
	}
	next := current
	next.at = current.at.Add(2 * time.Second)
	stopped := monitorProcess{}
	stopped.setRates(current, next)
	if stopped.WriteBPS == nil || *stopped.WriteBPS != 0 {
		t.Fatalf("写入停止后速率应为零：%+v", stopped)
	}
	next.startTicks++
	reused := monitorProcess{}
	reused.setRates(current, next)
	if reused.CPUPercent != nil || reused.WriteBPS != nil || reused.ReadBPS != nil {
		t.Fatalf("复用的 PID 不应沿用旧基线：%+v", reused)
	}
}

func TestServerMonitorLiveReopen(t *testing.T) {
	a, _ := testApp(t)
	for _, path := range []string{"/summary", "/disks", "/processes"} {
		w := call(a, http.MethodGet, "/api/v1/server/monitor"+path, "", nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("未登录访问 %s 返回 %d", path, w.Code)
		}
	}

	state := &a.monitorLive
	if _, err := state.readSample(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.readProcesses(); err != nil {
		t.Fatal(err)
	}
	state.sampleReadAt = time.Now().Add(-time.Minute)
	state.processReadAt = state.sampleReadAt

	sample, err := state.readSample()
	if err != nil || sample.CPUPercent != nil || sample.MemoryTotalBytes == 0 ||
		!slices.ContainsFunc(sample.Disks, func(disk monitorDisk) bool { return disk.Mount == "/" }) {
		t.Fatalf("重新打开后的整机采样错误：%+v，%v", sample, err)
	}
	processes, err := state.readProcesses()
	index := slices.IndexFunc(processes.Processes, func(process monitorProcess) bool { return process.PID == os.Getpid() })
	if err != nil || index < 0 {
		t.Fatalf("无法读取当前进程：%v", err)
	}
	process := processes.Processes[index]
	if process.CPUPercent != nil || process.ReadBPS != nil || process.WriteBPS != nil {
		t.Fatalf("复开后首个样本的进程速率应为空：%+v", process)
	}

	// 跳过一秒缓存，并令新基线产生可验证的 CPU 计数差。
	state.sampleReadAt = time.Now().Add(-2 * time.Second)
	state.cpu.busy--
	state.processReadAt = state.sampleReadAt
	sample, err = state.readSample()
	if err != nil || sample.CPUPercent == nil {
		t.Fatalf("后续采样应恢复计算整机 CPU：%+v，%v", sample, err)
	}
	processes, err = state.readProcesses()
	index = slices.IndexFunc(processes.Processes, func(process monitorProcess) bool { return process.PID == os.Getpid() })
	if err != nil || index < 0 || processes.Processes[index].CPUPercent == nil {
		t.Fatalf("后续采样应恢复计算进程 CPU：%v", err)
	}
}
