package panel

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func (a *App) transferServerFile(move bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		action := "复制"
		if move {
			action = "移动"
		}
		var input struct {
			Path      string `json:"path"`
			Directory string `json:"directory"`
		}
		if !readServerFileJSON(w, r, &input) {
			return
		}
		source := serverFilePath(w, input.Path, false)
		if source == "" {
			return
		}
		directory := serverFilePath(w, input.Directory, true)
		if directory == "" {
			return
		}
		info := serverFileOperationInfo(w, source, action)
		if info == nil {
			return
		}
		destinationInfo, err := os.Lstat(directory)
		if err != nil {
			writeServerFileError(w, err, action)
			return
		}
		if !destinationInfo.IsDir() {
			WriteError(w, http.StatusBadRequest, "not_directory", "只能粘贴到目录")
			return
		}
		if info.IsDir() {
			// 解析父目录别名，避免把目录复制到经由符号链接访问的下级。
			realSource, err := filepath.EvalSymlinks(source)
			if err != nil {
				writeServerFileError(w, err, action)
				return
			}
			realDirectory, err := filepath.EvalSymlinks(directory)
			if err != nil {
				writeServerFileError(w, err, action)
				return
			}
			if realDirectory == realSource || strings.HasPrefix(realDirectory, realSource+"/") {
				WriteError(w, http.StatusBadRequest, "destination_inside_source", "不能将目录粘贴到自身或下级目录")
				return
			}
		}
		target := filepath.Join(directory, filepath.Base(source))
		if move {
			err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
			if !errors.Is(err, unix.EXDEV) {
				if err != nil {
					writeServerFileError(w, err, action)
				} else {
					WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
				}
				return
			}
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				err = os.ErrExist
			}
			writeServerFileError(w, err, action)
			return
		}
		var sourceMount uint64
		if move {
			var err error
			sourceMount, err = serverFileMount(filepath.Dir(source), 0)
			if err != nil {
				writeServerFileError(w, err, action)
				return
			}
		}
		stage, err := os.MkdirTemp(directory, ".lpanel-copy-*")
		if err != nil {
			writeServerFileError(w, err, action)
			return
		}
		defer os.RemoveAll(stage)
		stageInfo, err := os.Stat(stage)
		if err != nil {
			writeServerFileError(w, err, action)
			return
		}
		stagedFile := filepath.Join(stage, "entry")
		err = validateServerFileTree(source, stageInfo, sourceMount)
		switch {
		case errors.Is(err, unix.ELOOP):
			WriteError(w, http.StatusBadRequest, "destination_inside_source", "不能将目录粘贴到自身或下级目录")
			return
		case errors.Is(err, unix.EXDEV):
			WriteError(w, http.StatusBadRequest, "nested_mount", "无法跨文件系统移动包含挂载点的条目")
			return
		case errors.Is(err, unix.ENOTSUP):
			WriteError(w, http.StatusBadRequest, "unsupported_file_type", "无法复制包含设备、管道等特殊文件的条目")
			return
		case err != nil:
			writeServerFileError(w, err, action)
			return
		}
		// 明确保留属性，目标不支持所需属性时复制失败，源条目保持原样。
		if _, err := a.Command(r.Context(), "cp", "--recursive", "--no-dereference", "--no-target-directory",
			"--preserve=mode,ownership,timestamps,links,xattr", "--", source, stagedFile); err != nil {
			WriteError(w, http.StatusInternalServerError, "file_copy_failed", "无法复制内容或保留文件属性，请检查目标目录的权限和文件系统")
			return
		}
		if err := unix.Renameat2(unix.AT_FDCWD, stagedFile, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); err != nil {
			writeServerFileError(w, err, action)
			return
		}
		if move {
			if err := os.RemoveAll(source); err != nil {
				WriteError(w, http.StatusInternalServerError, "move_source_cleanup_failed", "目标已保存，源文件或目录未能完全删除，请核对两处内容")
				return
			}
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func serverFileMount(path string, flags int) (uint64, error) {
	return serverFileMountAt(unix.AT_FDCWD, path, flags)
}

func serverFileMountAt(dirFD int, path string, flags int) (uint64, error) {
	var stat unix.Statx_t
	err := unix.Statx(dirFD, path, flags|unix.AT_NO_AUTOMOUNT, unix.STATX_MNT_ID, &stat)
	if err == nil && stat.Mask&unix.STATX_MNT_ID == 0 {
		err = unix.ENOSYS
	}
	return stat.Mnt_id, err
}

func validateServerFileTree(source string, stage os.FileInfo, sourceMount uint64) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if sourceMount != 0 {
			// 跨盘移动最后会清理源目录，先确认源树中没有其他挂载点。
			mount, err := serverFileMount(path, unix.AT_SYMLINK_NOFOLLOW)
			if err != nil {
				return err
			}
			if mount != sourceMount {
				return unix.EXDEV
			}
		}
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if os.SameFile(info, stage) {
				return unix.ELOOP
			}
		} else if !entry.Type().IsRegular() && entry.Type()&os.ModeSymlink == 0 {
			return unix.ENOTSUP
		}
		return nil
	})
}

func changeServerFileAttributes(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path      string  `json:"path"`
		Mode      *string `json:"mode"`
		Owner     *string `json:"owner"`
		Recursive bool    `json:"recursive"`
	}
	if !readServerFileJSON(w, r, &input) {
		return
	}
	path := serverFilePath(w, input.Path, false)
	if path == "" {
		return
	}
	action := "修改属性"
	if input.Mode == nil && input.Owner == nil {
		WriteError(w, http.StatusBadRequest, "missing_attributes", "请填写权限或所有者")
		return
	}
	var mode uint64
	if input.Mode != nil {
		var err error
		mode, err = strconv.ParseUint(*input.Mode, 8, 12)
		if err != nil || len(*input.Mode) > 4 {
			WriteError(w, http.StatusBadRequest, "invalid_mode", "请输入有效的八进制权限，例如 644 或 0755")
			return
		}
	}
	uid := -1
	if input.Owner != nil {
		owner, err := user.Lookup(*input.Owner)
		value := *input.Owner
		if err == nil {
			value = owner.Uid
		}
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil || id == 1<<32-1 {
			WriteError(w, http.StatusBadRequest, "invalid_owner", "请输入本机用户名或用户 ID")
			return
		}
		uid = int(id)
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		writeServerFileError(w, err, action)
		return
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		writeServerFileError(w, err, action)
		return
	}
	fileType := stat.Mode & unix.S_IFMT
	if fileType == unix.S_IFLNK && input.Mode != nil {
		WriteError(w, http.StatusBadRequest, "link_mode_unsupported", "不能修改符号链接的权限")
		return
	}

	recursive := input.Recursive && fileType == unix.S_IFDIR
	var mount uint64
	if recursive {
		mount, err = serverFileMountAt(fd, "", unix.AT_EMPTY_PATH)
		if err != nil {
			writeServerFileError(w, err, action)
			return
		}
	}
	changed := false
	var apply func(int, *unix.Stat_t) error
	apply = func(fd int, stat *unix.Stat_t) error {
		descriptorPath := fmt.Sprintf("/proc/self/fd/%d", fd)
		fileType := stat.Mode & unix.S_IFMT
		switch fileType {
		case unix.S_IFDIR:
			if recursive {
				// 后序应用目录属性，避免权限收紧后无法修改子项。
				dir, err := os.Open(descriptorPath)
				if err != nil {
					return err
				}
				names, err := dir.Readdirnames(-1)
				_ = dir.Close()
				if err != nil {
					return err
				}
				for _, name := range names {
					if err := changeServerFileChildAttributes(fd, name, mount, apply); err != nil {
						return err
					}
				}
			}
		case unix.S_IFREG, unix.S_IFLNK:
		default:
			return unix.ENOTSUP
		}
		ownerChanged := uid != -1 && uint32(uid) != stat.Uid
		if ownerChanged {
			action = "修改所有者"
			if err := unix.Fchownat(fd, "", uid, -1, unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			changed = true
		}
		// Linux 变更所有者时可能清除 setuid/setgid，单独更改所有者时恢复原权限。
		originalMode := uint64(stat.Mode & 07777)
		restoreMode := ownerChanged && fileType == unix.S_IFREG && originalMode&06000 != 0
		if fileType != unix.S_IFLNK && (input.Mode != nil || restoreMode) {
			action = "修改权限"
			targetMode := mode
			if input.Mode == nil {
				targetMode = originalMode
			}
			// 通过已打开的条目修改权限，兼容没有 fchmodat2 的内核。
			if err := unix.Chmod(descriptorPath, uint32(targetMode)); err != nil {
				return err
			}
			changed = true
		}
		return nil
	}
	if err := apply(fd, &stat); err != nil {
		switch {
		case changed && recursive:
			WriteError(w, http.StatusInternalServerError, "attributes_update_incomplete", "部分条目的权限或所有者已修改，请刷新并核对文件属性")
		case changed:
			WriteError(w, http.StatusInternalServerError, "attributes_update_incomplete", "所有者已修改，权限设置失败，请刷新并核对文件属性")
		case errors.Is(err, unix.ENOTSUP):
			WriteError(w, http.StatusBadRequest, "unsupported_file_type", "不能修改设备、管道等特殊文件的属性")
		default:
			writeServerFileError(w, err, action)
		}
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func changeServerFileChildAttributes(parentFD int, name string, mount uint64, apply func(int, *unix.Stat_t) error) error {
	fd, err := unix.Openat(parentFD, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	childMount, err := serverFileMountAt(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return err
	}
	if childMount != mount {
		return nil
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	return apply(fd, &stat)
}
