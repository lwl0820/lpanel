package panel

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type ProgramPort struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type ProgramRuntime struct {
	PID         int           `json:"pid"`
	CPUPercent  *float64      `json:"cpu_percent"`
	MemoryBytes *uint64       `json:"memory_bytes"`
	Connections *int          `json:"connections"`
	Ports       []ProgramPort `json:"ports"`
	Message     string        `json:"message"`
}

func (a *App) programRuntime(ctx context.Context, p Program) (ProgramRuntime, error) {
	p = a.programStatus(ctx, p)
	result := ProgramRuntime{PID: p.PID, Ports: []ProgramPort{}}
	if p.Status != "running" || p.PID <= 0 {
		result.Message = p.StatusMessage
		return result, nil
	}
	processes, err := a.monitorLive.readProcesses()
	if err == nil {
		for _, process := range processes.Processes {
			if process.PID == p.PID {
				result.CPUPercent = process.CPUPercent
				result.MemoryBytes = new(process.RSSBytes)
				break
			}
		}
	}
	if result.MemoryBytes == nil {
		result.Message = "暂时无法读取主进程资源"
	}
	ports, connections, err := processTCPPorts("/proc", strconv.Itoa(p.PID))
	if err != nil {
		return result, err
	}
	slices.SortFunc(ports, func(a, b ProgramPort) int {
		return cmp.Or(cmp.Compare(a.Port, b.Port), cmp.Compare(a.Address, b.Address))
	})
	result.Ports = append(result.Ports, slices.Compact(ports)...)
	result.Connections = new(connections)
	return result, nil
}

func (a *App) registerProgramRuntime() {
	const prefix = "GET /api/v1/programs/{id}"
	a.Mux.Handle(prefix+"/runtime", a.RequireAuth(http.HandlerFunc(a.getProgramRuntime)))
	a.Mux.Handle(prefix+"/logs/stream", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := a.requireProgram(w, r); ok {
			a.streamProgramLog(w, r, p)
		}
	})))
}

func (a *App) getProgramRuntime(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireProgram(w, r)
	if !ok {
		return
	}
	runtime, err := a.programRuntime(r.Context(), p)
	if err != nil {
		runtime.Message = "无法读取主进程的 TCP 状态：" + err.Error()
	}
	WriteJSON(w, http.StatusOK, runtime)
}

// 每次按当前文件大小读取，日志轮转或截短后从新文件的最近输出重新开始。
type programLogCursor struct {
	file   os.FileInfo
	offset int64
}

func (cursor *programLogCursor) read(path string) (string, string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", "", err
	}
	const window = 256 << 10
	end := info.Size()
	snapshot := cursor.file == nil || !os.SameFile(cursor.file, info) || end < cursor.offset || end-cursor.offset > window
	start := cursor.offset
	if snapshot {
		start = max(0, end-window)
	} else if start == end {
		return "", "", nil
	}
	data, err := io.ReadAll(io.NewSectionReader(file, start, end-start))
	if err != nil {
		return "", "", err
	}
	// 一次写入可能只包含中文字符的一部分，留到下一次读取再发送。
	for i := len(data) - 1; i >= max(0, len(data)-utf8.UTFMax); i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				data = data[:i]
			}
			break
		}
	}
	if !snapshot && len(data) == 0 {
		return "", "", nil
	}
	cursor.file, cursor.offset = info, start+int64(len(data))
	if snapshot {
		lines := bytes.SplitAfter(data, []byte("\n"))
		count := 200
		if len(lines[len(lines)-1]) == 0 {
			count++
		}
		data = bytes.Join(lines[max(0, len(lines)-count):], nil)
		return "snapshot", strings.ToValidUTF8(string(data), "�"), nil
	}
	return "append", strings.ToValidUTF8(string(data), "�"), nil
}

func (a *App) streamProgramLog(w http.ResponseWriter, r *http.Request, p Program) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	response := http.NewResponseController(w)
	send := func(event, value string) error {
		payload, err := json.Marshal(value)
		if err == nil {
			_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
		}
		if err == nil {
			err = response.Flush()
		}
		return err
	}
	path := a.programLogPath(p.ID)
	var cursor programLogCursor
	poll := time.Tick(time.Second)
	keepAlive := time.Tick(15 * time.Second)
	for first := true; ; first = false {
		event, output, err := cursor.read(path)
		if err != nil {
			_ = send("log-error", "无法读取程序日志："+err.Error())
			return
		}
		if first && event == "" {
			event = "snapshot"
		}
		if event != "" && send(event, output) != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-poll:
		case <-keepAlive:
			if message := a.sessionError(r); message != "" {
				_ = send("log-error", message)
				return
			}
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil || response.Flush() != nil {
				return
			}
		}
	}
}
