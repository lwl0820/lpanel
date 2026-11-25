package panel

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

var errServerFileArchiveEntry = errors.New("无法打包名称不兼容的条目、链接或特殊文件")

func compressServerFiles(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Directory string   `json:"directory"`
		Names     []string `json:"names"`
		Name      string   `json:"name"`
	}
	if !readServerFileJSON(w, r, &input) {
		return
	}
	directory := serverFilePath(w, input.Directory, true)
	if directory == "" {
		return
	}
	if !validArchivePath(input.Name) || path.Base(input.Name) != input.Name || !strings.EqualFold(path.Ext(input.Name), ".zip") {
		WriteError(w, http.StatusBadRequest, "invalid_archive_name", "请输入以 .zip 结尾的压缩包名称，不能包含路径")
		return
	}
	if len(input.Names) == 0 {
		WriteError(w, http.StatusBadRequest, "missing_archive_entries", "请先选择要压缩的文件或目录")
		return
	}
	seen := make(map[string]bool, len(input.Names))
	for _, name := range input.Names {
		if !validArchivePath(name) || path.Base(name) != name || seen[name] {
			WriteError(w, http.StatusBadRequest, "invalid_archive_entries", "请选择当前目录中名称有效且互不重复的文件或目录")
			return
		}
		seen[name] = true
	}

	dirFD, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		writeServerFileError(w, err, "打开目录")
		return
	}
	defer unix.Close(dirFD)
	var target unix.Stat_t
	err = unix.Fstatat(dirFD, input.Name, &target, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		err = os.ErrExist
	}
	if !errors.Is(err, os.ErrNotExist) {
		writeServerFileError(w, err, "创建压缩包")
		return
	}

	tempName := ".lpanel-compress-" + rand.Text()
	fd, err := unix.Openat(dirFD, tempName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0600)
	if err != nil {
		writeServerFileError(w, err, "创建压缩包")
		return
	}
	defer unix.Unlinkat(dirFD, tempName, 0)
	stage := os.NewFile(uintptr(fd), tempName)
	stageInfo, err := stage.Stat()
	if err == nil {
		writer := zip.NewWriter(stage)
		for _, name := range input.Names {
			err = addServerFileToArchive(r.Context(), writer, dirFD, name, name, stageInfo)
			if err != nil {
				break
			}
		}
		if err == nil {
			err = writer.Close()
		}
	}
	err = errors.Join(err, stage.Close())
	if errors.Is(err, errServerFileArchiveEntry) || errors.Is(err, unix.ELOOP) {
		WriteError(w, http.StatusBadRequest, "unsupported_archive_entry", errServerFileArchiveEntry.Error())
		return
	}
	if err == nil {
		err = r.Context().Err()
	}
	if err == nil {
		err = unix.Renameat2(dirFD, tempName, dirFD, input.Name, unix.RENAME_NOREPLACE)
	}
	if err != nil {
		writeServerFileError(w, err, "创建压缩包")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func addServerFileToArchive(ctx context.Context, writer *zip.Writer, dirFD int, name, archiveName string, stage os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validArchivePath(archiveName) {
		return errServerFileArchiveEntry
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(dirFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	fileType := stat.Mode & unix.S_IFMT
	flags := unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC
	switch fileType {
	case unix.S_IFDIR:
		flags |= unix.O_DIRECTORY
	case unix.S_IFREG:
	default:
		return errServerFileArchiveEntry
	}
	fd, err := unix.Openat(dirFD, name, flags, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if os.SameFile(info, stage) || (!info.IsDir() && !info.Mode().IsRegular()) {
		return errServerFileArchiveEntry
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = archiveName
	if !info.IsDir() {
		header.Method = zip.Deflate
		dst, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, file)
		return err
	}
	header.Name += "/"
	if _, err := writer.CreateHeader(header); err != nil {
		return err
	}
	children, err := file.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := addServerFileToArchive(ctx, writer, fd, child.Name(), archiveName+"/"+child.Name(), stage); err != nil {
			return err
		}
	}
	return nil
}
