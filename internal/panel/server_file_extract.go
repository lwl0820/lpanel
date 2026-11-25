package panel

import (
	"archive/zip"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

var errInvalidServerZIP = errors.New("无效的 ZIP 条目")

func extractServerFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path      string `json:"path"`
		Directory string `json:"directory"`
	}
	if !readServerFileJSON(w, r, &input) {
		return
	}
	path := serverFilePath(w, input.Path, false)
	if path == "" {
		return
	}
	directory := serverFilePath(w, input.Directory, true)
	if directory == "" {
		return
	}
	file := openServerRegularFile(w, path, os.O_RDONLY)
	if file == nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeServerFileError(w, err, "读取压缩包")
		return
	}
	if !info.Mode().IsRegular() {
		WriteError(w, http.StatusBadRequest, "not_regular_file", "只能解压普通文件")
		return
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_zip", "无法读取 ZIP 压缩包，或压缩包中有不安全的路径")
		return
	}

	target, missing, err := serverExtractionDestination(directory)
	if err != nil {
		writeServerFileError(w, err, "打开解压目录")
		return
	}
	defer target.Close()
	const action = "解压文件"
	targetFD := int(target.Fd())
	stagePath, err := os.MkdirTemp(fmt.Sprintf("/proc/self/fd/%d", targetFD), ".lpanel-extract-")
	if err != nil {
		writeServerFileError(w, err, action)
		return
	}
	defer func() {
		if err := os.RemoveAll(stagePath); err != nil {
			// 暂存目录可能已还原为不可访问，先逐层恢复目录权限再清理。
			walkErr := filepath.WalkDir(stagePath, func(path string, entry fs.DirEntry, err error) error {
				if err == nil && entry.IsDir() {
					return os.Chmod(path, 0700)
				}
				return err
			})
			if err := os.RemoveAll(stagePath); err != nil {
				log.Printf("清理解压暂存目录失败：%v", errors.Join(walkErr, err))
			}
		}
	}()
	stage, err := os.OpenFile(stagePath, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		writeServerFileError(w, err, action)
		return
	}
	defer stage.Close()

	stageFD := int(stage.Fd())
	unpackPath := fmt.Sprintf("/proc/self/fd/%d", stageFD)
	firstMissing, _, _ := strings.Cut(missing, "/")
	if firstMissing != "" {
		unpackPath = filepath.Join(unpackPath, missing)
		if err := os.MkdirAll(unpackPath, 0755); err != nil {
			writeServerFileError(w, err, action)
			return
		}
	}
	if err := unpackServerZIP(r.Context(), archive, unpackPath); err != nil {
		if errors.Is(err, errInvalidServerZIP) {
			WriteError(w, http.StatusBadRequest, "invalid_zip", "压缩包损坏，或包含无效、重复的路径与不支持的文件类型")
		} else {
			writeServerFileError(w, err, action)
		}
		return
	}
	if err := r.Context().Err(); err != nil {
		writeServerFileError(w, err, action)
		return
	}

	if firstMissing != "" {
		if err := unix.Renameat2(stageFD, firstMissing, targetFD, firstMissing, unix.RENAME_NOREPLACE); err != nil {
			writeServerFileError(w, err, action)
			return
		}
	} else {
		names, err := stage.Readdirnames(-1)
		if err != nil {
			writeServerFileError(w, err, action)
			return
		}
		// 先检查全部顶层名称，已存在的目录也算同名冲突。
		for _, name := range names {
			var info unix.Stat_t
			err := unix.Fstatat(targetFD, name, &info, unix.AT_SYMLINK_NOFOLLOW)
			if err == nil {
				err = os.ErrExist
			}
			if !errors.Is(err, os.ErrNotExist) {
				writeServerFileError(w, err, action)
				return
			}
		}
		for i, name := range names {
			if err := unix.Renameat2(stageFD, name, targetFD, name, unix.RENAME_NOREPLACE); err != nil {
				if i > 0 {
					WriteError(w, http.StatusInternalServerError, "extract_incomplete", "部分条目已解压，请检查解压目录")
					return
				}
				writeServerFileError(w, err, action)
				return
			}
		}
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// 返回已有目标目录，或最近的已有父目录及尚待创建的相对路径。
func serverExtractionDestination(directory string) (*os.File, string, error) {
	parent := directory
	missing := ""
	flags := unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	for {
		fd, err := unix.Open(parent, flags, 0)
		if err == nil {
			return os.NewFile(uintptr(fd), parent), missing, nil
		}
		if !errors.Is(err, os.ErrNotExist) || parent == "/" {
			return nil, "", err
		}
		missing = filepath.Join(filepath.Base(parent), missing)
		parent = filepath.Dir(parent)
		flags &^= unix.O_NOFOLLOW
	}
}

func unpackServerZIP(ctx context.Context, archive *zip.Reader, destination string) error {
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()

	seen := make(map[string]bool)
	directoryModes := make(map[string]os.FileMode)
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimPrefix(file.Name, "./")
		mode := file.Mode()
		if name == "" && file.Name == "./" && mode.Type() == os.ModeDir {
			continue
		}
		name = strings.TrimSuffix(name, "/")
		if !validArchivePath(name) || seen[name] || (mode.Type() != os.ModeDir && !mode.IsRegular()) {
			return errInvalidServerZIP
		}
		seen[name] = true
		parent := name
		if !mode.IsDir() {
			parent = filepath.Dir(name)
		}
		if err := root.MkdirAll(parent, 0755); err != nil {
			if errors.Is(err, unix.ENOTDIR) || errors.Is(err, os.ErrExist) {
				return errInvalidServerZIP
			}
			return err
		}
		// ZIP 创建平台为 Unix 或 macOS 时，才把 mode 视作原始 Unix 权限。
		system := file.CreatorVersion >> 8
		hasUnixMode := system == 3 || system == 19
		if mode.IsDir() {
			if hasUnixMode {
				directoryModes[name] = mode.Perm()
			}
			continue
		}
		in, err := file.Open()
		if err != nil {
			return errors.Join(errInvalidServerZIP, err)
		}
		out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
		if err != nil {
			_ = in.Close()
			if errors.Is(err, os.ErrExist) {
				return errInvalidServerZIP
			}
			return err
		}
		_, copyErr := io.Copy(out, in)
		err = errors.Join(copyErr, out.Close(), in.Close())
		if err != nil {
			_, corruptDeflate := errors.AsType[flate.CorruptInputError](err)
			if corruptDeflate || errors.Is(err, zip.ErrChecksum) || errors.Is(err, zip.ErrFormat) || errors.Is(err, io.ErrUnexpectedEOF) {
				return errors.Join(errInvalidServerZIP, err)
			}
			return err
		}
		if hasUnixMode {
			if err := root.Chmod(name, mode.Perm()); err != nil {
				return err
			}
		}
	}
	// 先恢复子目录，避免父目录权限收紧后无法访问其子项。
	for _, name := range slices.Backward(slices.Sorted(maps.Keys(directoryModes))) {
		if err := root.Chmod(name, directoryModes[name]); err != nil {
			return err
		}
	}
	return nil
}
