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

const swapMiB = 1 << 20

type swapRequest struct {
	Path    string `json:"path"`
	SizeMiB int64  `json:"size_mib"`
}

func (a *App) registerServerSwap() error {
	if _, err := a.DB.Exec("CREATE TABLE IF NOT EXISTS server_swap_files (path TEXT PRIMARY KEY)"); err != nil {
		return err
	}
	const route = "/api/v1/server/swap"
	a.Mux.Handle("GET "+route, a.RequireAuth(http.HandlerFunc(a.getServerSwap)))
	for _, action := range []string{"files", "resize", "enable", "disable", "delete"} {
		pattern := "POST " + route + "/" + action
		if action == "delete" {
			pattern = "DELETE " + route + "/files"
		}
		a.Mux.Handle(pattern, a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var input swapRequest
			if action == "delete" {
				input.Path = r.URL.Query().Get("path")
			} else if !ReadJSON(w, r, &input) {
				return
			}
			if (action == "files" || action == "resize") && (input.SizeMiB < 1 || input.SizeMiB > 1048576) {
				WriteError(w, 400, "invalid_swap_size", "容量须为 1 至 1048576 MiB 的整数")
				return
			}
			if action != "files" && (!filepath.IsAbs(input.Path) || filepath.Clean(input.Path) != input.Path || strings.ContainsRune(input.Path, 0)) {
				WriteError(w, 400, "invalid_swap_path", "请选择有效的交换文件")
				return
			}
			op, err := a.StartOperation("server_swap", func(ctx context.Context) (string, error) {
				a.swapMu.Lock()
				defer a.swapMu.Unlock()
				if err := a.changeServerSwap(ctx, action, input); err != nil {
					return "", err
				}
				return "交换空间设置已更新", nil
			})
			if err != nil {
				WriteError(w, 500, "operation_error", "无法启动交换空间操作")
				return
			}
			WriteJSON(w, http.StatusAccepted, op)
		})))
	}
	return nil
}

func (a *App) getServerSwap(w http.ResponseWriter, r *http.Request) {
	a.swapMu.Lock()
	defer a.swapMu.Unlock()
	state, err := a.readServerSwap(r.Context())
	if err != nil {
		WriteError(w, 500, "swap_read_error", "无法读取交换空间")
		return
	}
	WriteJSON(w, http.StatusOK, state)
}

func (a *App) changeServerSwap(ctx context.Context, action string, input swapRequest) error {
	state, err := a.readServerSwap(ctx)
	if err != nil {
		return err
	}
	if action == "files" {
		return a.createSwapFile(ctx, state.fstab, input.SizeMiB)
	}
	path := input.Path
	var entry swapEntry
	for _, item := range state.Entries {
		if item.Path == path {
			entry = item
			break
		}
	}
	if entry.Type != "file" || (action != "delete" && !entry.Managed) {
		return errors.New("请选择允许执行此操作的交换文件")
	}

	var stage string
	if action == "resize" {
		stage, err = a.prepareSwapFile(ctx, filepath.Dir(path), input.SizeMiB)
		if err != nil {
			return err
		}
		defer func() {
			if stage != "" {
				_ = os.Remove(stage)
			}
		}()
	}

	wasStopped := false
	restoreActive := func(err error) error {
		if !wasStopped {
			return err
		}
		restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		return errors.Join(err, a.runSwapCommand(restore, "swapon", "--", path))
	}
	if entry.Active && action != "enable" {
		if err := a.runSwapCommand(ctx, "swapoff", "--", path); err != nil {
			return err
		}
		wasStopped = true
		active, err := a.readActiveSwap()
		if err != nil {
			return restoreActive(err)
		}
		for _, current := range active {
			if current.identity == entry.identity {
				return errors.New("交换文件仍在使用")
			}
		}
	}

	switch action {
	case "enable":
		if !entry.Active {
			if err := a.runSwapCommand(ctx, "swapon", "--", path); err != nil {
				return err
			}
		}
		if err := a.writeSwapFstab(state.fstab, swapFstabWith(state.fstab, path, true)); err != nil {
			if !entry.Active {
				restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
				defer cancel()
				return errors.Join(err, a.runSwapCommand(restore, "swapoff", "--", path))
			}
			return err
		}
	case "resize":
		exchange := func() error {
			return unix.Renameat2(unix.AT_FDCWD, path, unix.AT_FDCWD, stage, unix.RENAME_EXCHANGE)
		}
		if err := exchange(); err != nil {
			return restoreActive(fmt.Errorf("无法替换交换文件：%w", err))
		}
		if entry.Active {
			if err := a.runSwapCommand(ctx, "swapon", "--", path); err != nil {
				if undoErr := exchange(); undoErr != nil {
					backup := stage
					stage = ""
					return errors.Join(err, fmt.Errorf("无法还原交换文件，旧文件保存在 %s：%w", backup, undoErr))
				}
				return restoreActive(err)
			}
		}
	case "disable", "delete":
		updated := swapFstabWith(state.fstab, path, false)
		if err := a.writeSwapFstab(state.fstab, updated); err != nil {
			return restoreActive(err)
		}
		if action == "delete" {
			if err := os.Remove(path); err != nil {
				return restoreActive(errors.Join(fmt.Errorf("无法删除交换文件：%w", err), a.writeSwapFstab(updated, state.fstab)))
			}
			if entry.Managed {
				_, err := a.DB.Exec("DELETE FROM server_swap_files WHERE path=?", path)
				return err
			}
		}
	}
	return nil
}

func (a *App) createSwapFile(ctx context.Context, fstab string, sizeMiB int64) error {
	dir := filepath.Join(a.Root, "data", "swap")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	stage, err := a.prepareSwapFile(ctx, dir, sizeMiB)
	if err != nil {
		return err
	}
	defer os.Remove(stage)
	path := filepath.Join(dir, filepath.Base(stage))
	if err := unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("无法保存交换文件：%w", err)
	}
	if _, err := a.DB.ExecContext(ctx, "INSERT INTO server_swap_files(path) VALUES(?)", path); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	remove := func(err error) error {
		if removeErr := os.Remove(path); removeErr != nil {
			return errors.Join(err, removeErr)
		}
		_, removeErr := a.DB.Exec("DELETE FROM server_swap_files WHERE path=?", path)
		return errors.Join(err, removeErr)
	}
	if err := a.runSwapCommand(ctx, "swapon", "--", path); err != nil {
		return remove(err)
	}
	if err := a.writeSwapFstab(fstab, swapFstabWith(fstab, path, true)); err != nil {
		restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if stopErr := a.runSwapCommand(restore, "swapoff", "--", path); stopErr != nil {
			return errors.Join(err, stopErr)
		}
		return remove(err)
	}
	return nil
}

func (a *App) prepareSwapFile(ctx context.Context, dir string, sizeMiB int64) (string, error) {
	stageDir := filepath.Join(dir, ".tmp")
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		return "", err
	}
	var destination, staging unix.Stat_t
	if err := unix.Stat(dir, &destination); err != nil {
		return "", err
	}
	if err := unix.Stat(stageDir, &staging); err != nil {
		return "", err
	}
	if destination.Dev != staging.Dev {
		return "", errors.New("交换文件和暂存目录必须位于同一文件系统")
	}
	var space unix.Statfs_t
	if err := unix.Statfs(stageDir, &space); err != nil {
		return "", fmt.Errorf("无法读取剩余磁盘空间：%w", err)
	}
	blockSize := uint64(space.Frsize)
	if blockSize == 0 {
		blockSize = uint64(space.Bsize)
	}
	size := uint64(sizeMiB) * swapMiB
	if space.Bavail < (size+blockSize-1)/blockSize {
		return "", errors.New("可用磁盘空间不足，请减小交换文件容量")
	}
	f, err := os.CreateTemp(stageDir, "swap-")
	if err != nil {
		return "", fmt.Errorf("无法创建交换文件：%w", err)
	}
	path := f.Name()
	err = f.Close()
	if err == nil {
		err = a.runSwapCommand(ctx, "dd", "if=/dev/zero", "of="+path, "bs=1M", "count="+strconv.FormatInt(sizeMiB, 10), "conv=notrunc,fsync", "status=none")
	}
	if err == nil {
		err = a.runSwapCommand(ctx, "mkswap", "--", path)
	}
	if err != nil {
		return "", errors.Join(err, os.Remove(path))
	}
	return path, nil
}

func (a *App) runSwapCommand(ctx context.Context, command string, args ...string) error {
	out, err := a.Command(ctx, command, args...)
	if err != nil {
		return fmt.Errorf("%w：%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
