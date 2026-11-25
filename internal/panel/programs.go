package panel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"uuid"
)

type Program struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Directory     string `json:"directory"`
	Entry         string `json:"entry"`
	Args          string `json:"args"`
	RunUser       string `json:"run_user"`
	Status        string `json:"status"`
	StatusMessage string `json:"status_message"`
	PID           int    `json:"pid"`
}

const programColumns = "id,name,entry,args,run_user"

func (a *App) registerPrograms() error {
	if _, err := a.DB.Exec(`CREATE TABLE IF NOT EXISTS programs (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, entry TEXT NOT NULL, args TEXT NOT NULL, run_user TEXT NOT NULL
	)`); err != nil {
		return err
	}
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /api/v1/programs":                        a.listPrograms,
		"POST /api/v1/programs":                       a.createProgram,
		"POST /api/v1/programs/supervisor/install":    a.startSupervisorInstallation,
		"GET /api/v1/programs/{id}":                   a.getProgram,
		"PUT /api/v1/programs/{id}":                   a.updateProgram,
		"POST /api/v1/programs/{id}/actions/{action}": a.programAction,
		"POST /api/v1/programs/{id}/copy":             a.copyProgram,
		"DELETE /api/v1/programs/{id}":                a.deleteProgram,
	} {
		a.Mux.Handle(pattern, a.RequireAuth(handler))
	}
	a.registerProgramRuntime()
	return nil
}

func (a *App) programDirectory(id string) string {
	return filepath.Join(a.Root, "programs", id)
}

func checkProgramDirectory(dir string) error {
	for _, path := range []string{filepath.Dir(dir), dir} {
		if err := checkProgramDirectoryPath(path); err != nil {
			return err
		}
	}
	return nil
}

func checkProgramDirectoryPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("无法打开程序目录：%w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("程序目录不能是符号链接或其他文件：%s", path)
	}
	return nil
}

func (a *App) scanProgram(row interface{ Scan(...any) error }) (Program, error) {
	var p Program
	err := row.Scan(&p.ID, &p.Name, &p.Entry, &p.Args, &p.RunUser)
	p.Directory = a.programDirectory(p.ID)
	return p, err
}

func (a *App) program(ctx context.Context, id string) (Program, error) {
	return a.scanProgram(a.DB.QueryRowContext(ctx, "SELECT "+programColumns+" FROM programs WHERE id=?", id))
}

func (a *App) programs(ctx context.Context) ([]Program, error) {
	items := []Program{}
	rows, err := a.DB.QueryContext(ctx, "SELECT "+programColumns+" FROM programs ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := a.scanProgram(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (a *App) listPrograms(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items, err := a.programs(ctx)
	if err != nil {
		WriteError(w, 500, "program_error", "读取程序列表失败")
		return
	}
	dependencyError := ""
	if err := a.checkProgramSupervisor(ctx); err != nil {
		dependencyError = err.Error()
		for i := range items {
			items[i].Status, items[i].StatusMessage = "unknown", dependencyError
		}
	} else {
		out, err := a.supervisorCommand(ctx, "status")
		statuses := make(map[string]string)
		for line := range strings.SplitSeq(out, "\n") {
			if fields := strings.Fields(line); len(fields) >= 2 && strings.HasPrefix(fields[0], "lpanel-program-") {
				statuses[fields[0]] = line
			}
		}
		for i := range items {
			p := items[i]
			if line := statuses[programSupervisorName(p.ID)]; line != "" {
				items[i] = supervisorProgramStatus(p, line)
			} else {
				if _, statErr := os.Stat(a.programSupervisorPath(p.ID)); errors.Is(statErr, os.ErrNotExist) {
					items[i].Status = "stopped"
				} else if err != nil && out == "" {
					items[i].Status, items[i].StatusMessage = "unknown", "无法读取程序状态"
				} else {
					items[i].Status, items[i].StatusMessage = "unknown", "Supervisor 未加载程序配置"
				}
			}
		}
	}
	WriteJSON(w, 200, map[string]any{"programs": items, "dependency_error": dependencyError})
}

func (a *App) requireProgram(w http.ResponseWriter, r *http.Request) (Program, bool) {
	p, err := a.program(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		WriteError(w, 404, "not_found", "程序不存在")
	} else if err != nil {
		WriteError(w, 500, "program_error", "读取程序失败")
	}
	return p, err == nil
}

func (a *App) getProgram(w http.ResponseWriter, r *http.Request) {
	if p, ok := a.requireProgram(w, r); ok {
		WriteJSON(w, 200, a.programStatus(r.Context(), p))
	}
}

func defaultProgramUser() (string, error) {
	if os.Geteuid() == 0 {
		return "nobody", nil
	}
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		return "", err
	}
	return account.Username, nil
}

func programUser(name string) (int, int, error) {
	if name == "" || strings.ContainsAny(name, "\r\n\x00;#%") {
		return 0, 0, errors.New("请输入有效的系统运行用户")
	}
	account, err := user.Lookup(name)
	if err != nil {
		return 0, 0, fmt.Errorf("找不到系统运行用户 %q", name)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return 0, 0, err
	}
	gid, err := strconv.Atoi(account.Gid)
	return uid, gid, err
}

func (a *App) createProgram(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	p := Program{Name: strings.TrimSpace(input.Name)}
	if p.Name == "" {
		WriteError(w, 400, "invalid_program", "请输入程序名称")
		return
	}
	var err error
	p.RunUser, err = defaultProgramUser()
	if err != nil {
		WriteError(w, 500, "program_error", "无法确定默认运行用户")
		return
	}
	a.programsMu.Lock()
	defer a.programsMu.Unlock()
	if _, err = a.addProgram(r.Context(), &p, ""); err != nil {
		writeProgramSaveError(w, "创建程序失败", err)
		return
	}
	WriteJSON(w, 201, p)
}

func writeProgramSaveError(w http.ResponseWriter, message string, err error) {
	if isUniqueConstraint(err) {
		WriteError(w, 409, "program_name_conflict", "已有同名程序")
		return
	}
	WriteError(w, 500, "program_error", fmt.Sprintf("%s：%v", message, err))
}

func (a *App) addProgram(ctx context.Context, p *Program, sourceDirectory string) (int, error) {
	uid, gid, err := programUser(p.RunUser)
	if err != nil {
		return 0, err
	}
	p.ID = uuid.NewV4().String()
	p.Directory = a.programDirectory(p.ID)
	p.Status = "stopped"
	if err = os.MkdirAll(filepath.Dir(p.Directory), 0755); err != nil {
		return 0, err
	}
	if err = checkProgramDirectoryPath(filepath.Dir(p.Directory)); err != nil {
		return 0, err
	}
	if err = os.Mkdir(p.Directory, 0750); err != nil {
		return 0, err
	}
	kept := false
	defer func() {
		if !kept {
			_ = os.RemoveAll(p.Directory)
		}
	}()
	skipped := 0
	if sourceDirectory != "" {
		if err = checkProgramDirectory(sourceDirectory); err != nil {
			return 0, err
		}
		if err = checkDirectoryMounts(sourceDirectory, "程序"); err != nil {
			return 0, err
		}
		skipped, err = copyProgramFiles(ctx, sourceDirectory, p.Directory, uid, gid)
		if err != nil {
			return 0, err
		}
	}
	if os.Geteuid() == 0 {
		if err = os.Chown(p.Directory, uid, gid); err != nil {
			return 0, err
		}
	}
	if _, err = a.DB.ExecContext(ctx, "INSERT INTO programs("+programColumns+") VALUES(?,?,?,?,?)", p.ID, p.Name, p.Entry, p.Args, p.RunUser); err != nil {
		return 0, err
	}
	kept = true
	return skipped, nil
}

func (a *App) updateProgram(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name    string `json:"name"`
		Entry   string `json:"entry"`
		Args    string `json:"args"`
		RunUser string `json:"run_user"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	a.programsMu.Lock()
	defer a.programsMu.Unlock()
	p, ok := a.requireProgram(w, r)
	if !ok {
		return
	}
	p.Name, p.Entry, p.Args, p.RunUser = strings.TrimSpace(input.Name), strings.TrimSpace(input.Entry), input.Args, strings.TrimSpace(input.RunUser)
	if p.Name == "" {
		WriteError(w, 400, "invalid_program", "请输入程序名称")
		return
	}
	if _, _, err := programUser(p.RunUser); err != nil {
		WriteError(w, 400, "invalid_program", err.Error())
		return
	}
	if _, err := splitProgramArgs(p.Args); err != nil {
		WriteError(w, 400, "invalid_program", err.Error())
		return
	}
	if p.Entry != "" {
		if err := prepareProgramEntry(p); err != nil {
			WriteError(w, 400, "invalid_program", err.Error())
			return
		}
	}
	ctx := r.Context()
	if _, err := a.DB.ExecContext(ctx, "UPDATE programs SET name=?,entry=?,args=?,run_user=? WHERE id=?", p.Name, p.Entry, p.Args, p.RunUser, p.ID); err != nil {
		writeProgramSaveError(w, "保存启动设置失败", err)
		return
	}
	WriteJSON(w, 200, a.programStatus(ctx, p))
}

func (a *App) copyProgram(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	a.programsMu.Lock()
	defer a.programsMu.Unlock()
	p, ok := a.requireProgram(w, r)
	if !ok {
		return
	}
	p.Name = strings.TrimSpace(input.Name)
	if p.Name == "" {
		WriteError(w, 400, "invalid_program", "请输入副本名称")
		return
	}
	skipped, err := a.addProgram(r.Context(), &p, p.Directory)
	if err != nil {
		writeProgramSaveError(w, "复制程序失败", err)
		return
	}
	WriteJSON(w, 201, struct {
		Program      Program `json:"program"`
		SkippedLinks int     `json:"skipped_links"`
	}{p, skipped})
}

func copyProgramFiles(ctx context.Context, from, to string, uid, gid int) (int, error) {
	root, err := os.OpenRoot(from)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	skipped := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || name == "." {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			skipped++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(to, name)
		switch {
		case info.IsDir():
			if err := os.Mkdir(target, info.Mode().Perm()); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			src, err := root.Open(name)
			if err != nil {
				return err
			}
			defer src.Close()
			dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
			if err != nil {
				return err
			}
			_, err = io.Copy(dst, src)
			if err = errors.Join(err, dst.Close()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("无法复制特殊文件 %q", name)
		}
		if err := os.Chmod(target, info.Mode().Perm()); err != nil {
			return err
		}
		if os.Geteuid() == 0 {
			return os.Chown(target, uid, gid)
		}
		return nil
	})
	return skipped, err
}

func (a *App) deleteProgram(w http.ResponseWriter, r *http.Request) {
	a.programsMu.Lock()
	defer a.programsMu.Unlock()
	p, ok := a.requireProgram(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	removeFiles := r.URL.Query().Get("remove_files") == "true"
	if removeFiles {
		err := checkProgramDirectory(p.Directory)
		if err == nil {
			err = checkDirectoryMounts(p.Directory, "程序")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			WriteError(w, 400, "invalid_directory", err.Error())
			return
		}
	}
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	if err := a.removeProgramSupervisor(ctx, p); err != nil {
		WriteError(w, 502, "supervisor_error", err.Error())
		return
	}
	if err := a.deleteProgramBindingsLocked(ctx, p.ID); err != nil {
		WriteError(w, 500, "program_error", fmt.Sprintf("解除程序域名失败：%v", err))
		return
	}
	if removeFiles {
		if err := os.RemoveAll(p.Directory); err != nil {
			WriteError(w, 500, "program_error", fmt.Sprintf("删除程序目录失败：%v", err))
			return
		}
	}
	if err := os.Remove(a.programLogPath(p.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		WriteError(w, 500, "program_error", fmt.Sprintf("删除程序日志失败：%v", err))
		return
	}
	if _, err := a.DB.ExecContext(ctx, "DELETE FROM programs WHERE id=?", p.ID); err != nil {
		WriteError(w, 500, "program_error", "删除程序记录失败")
		return
	}
	WriteJSON(w, 200, map[string]string{"message": "已删除程序"})
}
