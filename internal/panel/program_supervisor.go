package panel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func programSupervisorName(id string) string { return "lpanel-program-" + id }

func (a *App) programSupervisorPath(id string) string {
	return filepath.Join(a.programSupervisorDir, programSupervisorName(id)+".conf")
}

func (a *App) programLogPath(id string) string {
	return filepath.Join(a.Root, "data", "program-logs", id+".log")
}

func (a *App) supervisorCommand(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := a.Command(ctx, "supervisorctl", append([]string{"-c", a.programSupervisorConfig}, args...)...)
	output := strings.TrimSpace(string(out))
	if err != nil {
		if output != "" {
			return output, fmt.Errorf("Supervisor %s 失败：%s", args[0], output)
		}
		return output, err
	}
	return output, nil
}

func (a *App) checkProgramSupervisor(ctx context.Context) error {
	out, err := a.supervisorCommand(ctx, "pid")
	if err == nil {
		if pid, err := strconv.Atoi(out); err == nil && pid > 0 {
			return nil
		}
		return fmt.Errorf("Supervisor 未返回服务进程：%s", out)
	}
	return fmt.Errorf("无法连接 Supervisor，请安装并启动 Supervisor 服务：%w", err)
}

func supervisorProgramStatus(p Program, output string) Program {
	p.Status, p.StatusMessage, p.PID = "unknown", "", 0
	fields := strings.Fields(output)
	if len(fields) < 2 {
		p.StatusMessage = "无法读取程序状态"
		return p
	}
	switch fields[1] {
	case "RUNNING":
		p.Status = "running"
		if len(fields) > 3 && fields[2] == "pid" {
			p.PID, _ = strconv.Atoi(strings.TrimSuffix(fields[3], ","))
		}
	case "STOPPED":
		p.Status = "stopped"
	case "STARTING", "BACKOFF":
		p.Status = "starting"
	case "STOPPING":
		p.Status = "stopping"
	case "FATAL", "EXITED":
		p.Status = "failed"
	}
	if p.Status == "starting" || p.Status == "failed" || p.Status == "unknown" {
		p.StatusMessage = strings.Join(fields[2:], " ")
	}
	return p
}

func (a *App) programStatus(ctx context.Context, p Program) Program {
	if _, err := os.Stat(a.programSupervisorPath(p.ID)); errors.Is(err, os.ErrNotExist) {
		p.Status = "stopped"
		return p
	}
	name := programSupervisorName(p.ID)
	out, err := a.supervisorCommand(ctx, "status", name)
	if strings.HasPrefix(out, name+" ") {
		return supervisorProgramStatus(p, out)
	}
	p.Status, p.StatusMessage, p.PID = "unknown", "无法读取程序状态", 0
	if strings.HasPrefix(out, name+": ERROR (no such group)") {
		p.StatusMessage = "Supervisor 未加载程序配置"
	} else if err != nil {
		p.StatusMessage = err.Error()
	}
	return p
}

func prepareProgramEntry(p Program) error {
	if p.Entry == "" {
		return errors.New("请先在启动设置中填写主程序路径")
	}
	entry := filepath.Clean(p.Entry)
	if !filepath.IsLocal(p.Entry) || entry == "." || strings.ContainsAny(p.Entry, "\r\n\x00") {
		return errors.New("主程序必须使用工作目录内的相对路径")
	}
	if err := checkProgramDirectory(p.Directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(p.Directory)
	if err != nil {
		return fmt.Errorf("无法打开程序目录：%w", err)
	}
	defer root.Close()
	info, err := root.Lstat(entry)
	if err != nil {
		return fmt.Errorf("无法找到主程序：%w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("主程序必须是普通文件")
	}
	// 使用已打开的文件设置权限，路径始终受程序目录约束。
	file, err := root.OpenFile(entry, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("无法打开主程序：%w", err)
	}
	defer file.Close()
	if err := file.Chmod(info.Mode().Perm() | 0555); err != nil {
		return fmt.Errorf("无法设置主程序执行权限：%w", err)
	}
	return nil
}

func splitProgramArgs(input string) ([]string, error) {
	if strings.ContainsAny(input, "\r\n\x00") {
		return nil, errors.New("启动参数必须写在同一行")
	}
	var args []string
	var word strings.Builder
	var quote byte
	started := false
	for i := 0; i < len(input); i++ {
		b := input[i]
		switch {
		case b == '\\' && quote != '\'':
			if i+1 == len(input) {
				return nil, errors.New("启动参数末尾的反斜杠需要转义")
			}
			if quote == 0 || input[i+1] == '"' || input[i+1] == '\\' {
				i++
				b = input[i]
			}
			word.WriteByte(b)
			started = true
		case quote != 0:
			if b == quote {
				quote = 0
			} else {
				word.WriteByte(b)
			}
		case b == '\'' || b == '"':
			quote = b
			started = true
		case b == ' ' || b == '\t':
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteByte(b)
			started = true
		}
	}
	if quote != 0 {
		return nil, errors.New("启动参数中的引号未闭合")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}

func quoteSupervisorArg(arg string) string {
	// Supervisor 先读取 INI，再按 shlex 分词。相邻的引号段保护注释和特殊展开标记。
	arg = strings.NewReplacer(
		`\`, `\\`, `"`, `\"`, ";", `"";`, "#", `""#`, "%(here)s", `%%(""here)s`, "%", "%%",
	).Replace(arg)
	return `"` + arg + `"`
}

func (a *App) programSupervisorFile(p Program) ([]byte, error) {
	args, err := splitProgramArgs(p.Args)
	if err != nil {
		return nil, err
	}
	command := []string{quoteSupervisorArg(filepath.Join(p.Directory, p.Entry))}
	for _, arg := range args {
		command = append(command, quoteSupervisorArg(arg))
	}
	logPath := a.programLogPath(p.ID)
	for _, path := range []string{p.Directory, logPath} {
		if strings.TrimSpace(path) != path || strings.ContainsAny(path, "\r\n\x00;#") || strings.Contains(path, "%(here)s") {
			return nil, errors.New("面板目录含 Supervisor 无法识别的字符")
		}
	}
	encodePath := func(path string) string { return strings.ReplaceAll(path, "%", "%%") }
	content := fmt.Sprintf(`# 本文件由 LPanel 管理
[program:%s]
command=%s
directory=%s
user=%s
autostart=false
autorestart=true
startsecs=1
startretries=3
stopasgroup=true
killasgroup=true
redirect_stderr=true
stdout_logfile=%s
stdout_logfile_maxbytes=10MB
stdout_logfile_backups=0
`, programSupervisorName(p.ID), strings.Join(command, " "), encodePath(p.Directory), p.RunUser, encodePath(logPath))
	return []byte(content), nil
}

func writeProgramSupervisorFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".lpanel-program-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (a *App) installProgramSupervisor(ctx context.Context, p Program) error {
	uid, gid, err := programUser(p.RunUser)
	if err != nil {
		return err
	}
	if err = prepareProgramEntry(p); err != nil {
		return err
	}
	content, err := a.programSupervisorFile(p)
	if err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err = os.Chown(p.Directory, uid, gid); err != nil {
			return fmt.Errorf("无法将程序目录交给运行用户：%w", err)
		}
	}
	if err = os.MkdirAll(filepath.Dir(a.programLogPath(p.ID)), 0700); err != nil {
		return err
	}
	path := a.programSupervisorPath(p.ID)
	previous, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = writeProgramSupervisorFile(path, content); err != nil {
		return fmt.Errorf("无法写入 Supervisor 配置，请确认已安装 Supervisor：%w", err)
	}
	name := programSupervisorName(p.ID)
	if _, err = a.supervisorCommand(ctx, "update", name); err == nil {
		// 验证配置已被 Supervisor 的 include 规则读取。
		out, _ := a.supervisorCommand(ctx, "status", name)
		if strings.HasPrefix(out, name+" ") && !strings.Contains(out, "(no such process)") {
			return nil
		}
		err = errors.New("Supervisor 未加载程序配置，请检查配置目录的 include 设置")
	}
	if previous != nil {
		err = errors.Join(err, writeProgramSupervisorFile(path, previous))
	} else {
		err = errors.Join(err, os.Remove(path))
	}
	return err
}

func (a *App) programAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "start", "stop", "restart":
	default:
		WriteError(w, 400, "invalid_action", "请选择启动、停止或重启")
		return
	}
	a.programsMu.Lock()
	defer a.programsMu.Unlock()
	p, ok := a.requireProgram(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := a.checkProgramSupervisor(ctx); err != nil {
		WriteError(w, 503, "supervisor_unavailable", err.Error())
		return
	}
	status := a.programStatus(ctx, p)
	if (action == "stop" && status.Status == "stopped") || (action == "start" && status.Status == "running") {
		WriteJSON(w, 200, status)
		return
	}
	if action != "stop" {
		if p.Entry == "" {
			WriteError(w, 400, "invalid_program", "请先在启动设置中填写主程序路径")
			return
		}
		if err := a.installProgramSupervisor(ctx, p); err != nil {
			WriteError(w, 502, "supervisor_error", err.Error())
			return
		}
		if action == "restart" && a.programStatus(ctx, p).Status != "running" {
			action = "start"
		}
	}
	name := programSupervisorName(p.ID)
	if out, err := a.supervisorCommand(ctx, action, name); err != nil &&
		!(action == "stop" && strings.Contains(out, "(not running)")) {
		status = a.programStatus(ctx, p)
		message := err.Error()
		if status.StatusMessage != "" {
			message += "。 " + status.StatusMessage
		}
		WriteError(w, 502, "supervisor_error", message)
		return
	}
	WriteJSON(w, 200, a.programStatus(ctx, p))
}

func (a *App) removeProgramSupervisor(ctx context.Context, p Program) error {
	path := a.programSupervisorPath(p.ID)
	previous, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := a.checkProgramSupervisor(ctx); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	name := programSupervisorName(p.ID)
	if out, err := a.supervisorCommand(ctx, "update", name); err != nil &&
		!strings.Contains(out, "no such group: "+name) {
		return errors.Join(err, writeProgramSupervisorFile(path, previous))
	}
	return nil
}
