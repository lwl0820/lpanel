package panel

import (
	json "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	serverFileTextLimit     = 1 << 20
	serverFileTextJSONLimit = 7 << 20
)

type serverFileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

func (a *App) registerServerFiles() {
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /api/v1/server/files/list":     listServerFiles,
		"GET /api/v1/server/files/download": downloadServerFile,
		"GET /api/v1/server/files/text":     serverFileText,
		"PUT /api/v1/server/files/text":     serverFileText,
		"PUT /api/v1/server/files/upload":   uploadServerFile,
		"POST /api/v1/server/files/mkdir":   makeServerDirectory,
		"POST /api/v1/server/files/rename":  renameServerFile,
		"DELETE /api/v1/server/files":       deleteServerFile,
	} {
		a.Mux.Handle(pattern, a.RequireAuth(handler))
	}
}

func serverFilePath(w http.ResponseWriter, raw string, allowRoot bool) string {
	if !filepath.IsAbs(raw) || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		WriteError(w, http.StatusBadRequest, "invalid_path", "请输入以 / 开头的有效路径")
		return ""
	}
	path := filepath.Clean(raw)
	if !allowRoot && path == "/" {
		WriteError(w, http.StatusBadRequest, "invalid_path", "不能对根目录执行此操作")
		return ""
	}
	return path
}

func writeServerFileError(w http.ResponseWriter, err error, action string) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		WriteError(w, http.StatusNotFound, "not_found", "文件或目录不存在")
	case errors.Is(err, unix.ENOTEMPTY):
		WriteError(w, http.StatusConflict, "directory_not_empty", "只能删除空目录，请先清空目录")
	case errors.Is(err, os.ErrExist):
		WriteError(w, http.StatusConflict, "already_exists", "已有同名文件或目录")
	case errors.Is(err, os.ErrPermission), errors.Is(err, unix.EROFS):
		WriteError(w, http.StatusForbidden, "permission_denied", "没有权限"+action)
	case errors.Is(err, unix.ENOTDIR):
		WriteError(w, http.StatusBadRequest, "not_directory", "路径中包含不是目录的项目")
	case errors.Is(err, unix.ENAMETOOLONG), errors.Is(err, unix.EINVAL):
		WriteError(w, http.StatusBadRequest, "invalid_path", "路径或名称无效")
	default:
		WriteError(w, http.StatusInternalServerError, "file_operation_failed", action+"失败")
	}
}

func listServerFiles(w http.ResponseWriter, r *http.Request) {
	path := serverFilePath(w, r.URL.Query().Get("path"), true)
	if path == "" {
		return
	}
	info, err := os.Lstat(path)
	if err != nil {
		writeServerFileError(w, err, "读取目录")
		return
	}
	if !info.IsDir() {
		WriteError(w, http.StatusBadRequest, "not_directory", "只能打开目录")
		return
	}
	children, err := os.ReadDir(path)
	if err != nil {
		writeServerFileError(w, err, "读取目录")
		return
	}
	entries := make([]serverFileEntry, 0, len(children))
	skippedNames := 0
	for _, child := range children {
		if !utf8.ValidString(child.Name()) {
			skippedNames++
			continue
		}
		info, err := child.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			writeServerFileError(w, err, "读取目录")
			return
		}
		kind := "other"
		switch {
		case info.IsDir():
			kind = "dir"
		case info.Mode().IsRegular():
			kind = "file"
		case info.Mode()&os.ModeSymlink != 0:
			kind = "link"
		}
		entries = append(entries, serverFileEntry{
			Name: child.Name(), Path: filepath.Join(path, child.Name()), Kind: kind,
			Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, struct {
		Path         string            `json:"path"`
		Entries      []serverFileEntry `json:"entries"`
		SkippedNames int               `json:"skipped_names"`
	}{Path: path, Entries: entries, SkippedNames: skippedNames})
}

func openServerRegularFile(w http.ResponseWriter, path string, flags int) *os.File {
	info, err := os.Lstat(path)
	if err == nil && info.Mode().IsRegular() {
		var file *os.File
		file, err = os.OpenFile(path, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err == nil {
			return file
		}
	}
	if err == nil || errors.Is(err, unix.ELOOP) {
		WriteError(w, http.StatusBadRequest, "not_regular_file", "只能打开普通文件，不能打开链接、目录或特殊文件")
	} else {
		writeServerFileError(w, err, "打开文件")
	}
	return nil
}

func downloadServerFile(w http.ResponseWriter, r *http.Request) {
	path := serverFilePath(w, r.URL.Query().Get("path"), false)
	if path == "" {
		return
	}
	file := openServerRegularFile(w, path, os.O_RDONLY)
	if file == nil {
		return
	}
	defer file.Close()
	sendServerFileDownload(w, r, filepath.Base(path), file)
}

func sendServerFileDownload(w http.ResponseWriter, r *http.Request, name string, content io.Reader) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, content); err != nil {
			// 已开始发送附件，异常结束传输，让客户端识别不完整的下载。
			panic(http.ErrAbortHandler)
		}
	}
}

func validServerFileText(w http.ResponseWriter, text string) bool {
	if len(text) > serverFileTextLimit {
		WriteError(w, http.StatusRequestEntityTooLarge, "text_too_large", "只能编辑不超过 1 MiB 的文本文件")
		return false
	}
	if !utf8.ValidString(text) || strings.ContainsFunc(text, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	}) {
		WriteError(w, http.StatusBadRequest, "invalid_text", "只能编辑 UTF-8 编码的纯文本文件")
		return false
	}
	return true
}

func serverFileText(w http.ResponseWriter, r *http.Request) {
	path := serverFilePath(w, r.URL.Query().Get("path"), false)
	if path == "" {
		return
	}
	var input struct {
		Text *string `json:"text"`
	}
	flags := os.O_RDONLY
	if r.Method == http.MethodPut {
		flags = os.O_RDWR
		// 一个 ASCII 字节最多转义为六个 JSON 字节，文件大小按解码结果计算。
		err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, serverFileTextJSONLimit), &input)
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			WriteError(w, http.StatusRequestEntityTooLarge, "request_too_large", "请求内容过大")
			return
		}
		if err != nil || input.Text == nil {
			WriteError(w, http.StatusBadRequest, "invalid_json", "请输入有效的文本内容")
			return
		}
		if !validServerFileText(w, *input.Text) {
			return
		}
	}
	file := openServerRegularFile(w, path, flags)
	if file == nil {
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, serverFileTextLimit+1))
	if err != nil {
		_ = file.Close()
		writeServerFileError(w, err, "读取文本")
		return
	}
	text := string(content)
	if !validServerFileText(w, text) {
		_ = file.Close()
		return
	}
	if r.Method != http.MethodPut {
		_ = file.Close()
		w.Header().Set("Cache-Control", "no-store")
		WriteJSON(w, http.StatusOK, map[string]string{"text": text})
		return
	}
	saveServerFileText(w, file, *input.Text)
}

// 原地写入保留所有者和权限；写入一旦开始，后续失败不能保证原内容完整。
func saveServerFileText(w http.ResponseWriter, file interface {
	io.WriterAt
	Truncate(int64) error
	io.Closer
}, text string) {
	written, writeErr := file.WriteAt([]byte(text), 0)
	err := writeErr
	if err == nil {
		err = file.Truncate(int64(len(text)))
	}
	if err = errors.Join(err, file.Close()); err != nil {
		if written > 0 || writeErr == nil {
			WriteError(w, http.StatusInternalServerError, "text_save_incomplete", "保存未完成，文件内容可能已改变，请重新打开并核对原文件")
		} else {
			writeServerFileError(w, err, "保存文本")
		}
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func uploadServerFile(w http.ResponseWriter, r *http.Request) {
	path := serverFilePath(w, r.URL.Query().Get("path"), false)
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		writeServerFileError(w, err, "上传文件")
		return
	}
	_, copyErr := io.Copy(file, r.Body)
	if err = errors.Join(copyErr, file.Close()); err != nil {
		_ = os.Remove(path)
		writeServerFileError(w, err, "上传文件")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func readServerFileJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(params) > 1 || len(params) == 1 && params["charset"] == "" {
		WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "请使用 JSON 格式提交请求")
		return false
	}
	// 路径不能用替换字符修复，否则可能误操作名称中确实含有该字符的文件。
	if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, 2<<20), value); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", "请求内容格式错误")
		return false
	}
	return true
}

func makeServerDirectory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if !readServerFileJSON(w, r, &input) {
		return
	}
	path := serverFilePath(w, input.Path, false)
	if path == "" {
		return
	}
	if err := os.Mkdir(path, 0777); err != nil {
		writeServerFileError(w, err, "创建目录")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func renameServerFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !readServerFileJSON(w, r, &input) {
		return
	}
	path := serverFilePath(w, input.Path, false)
	if path == "" {
		return
	}
	if input.Name == "" || input.Name == "." || input.Name == ".." || strings.ContainsAny(input.Name, "/\x00") {
		WriteError(w, http.StatusBadRequest, "invalid_name", "请输入有效的名称，不能包含路径")
		return
	}
	if !serverFileCanRenameOrDelete(w, path, "重命名") {
		return
	}
	target := filepath.Join(filepath.Dir(path), input.Name)
	if err := unix.Renameat2(unix.AT_FDCWD, path, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); err != nil {
		writeServerFileError(w, err, "重命名")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func serverFileCanRenameOrDelete(w http.ResponseWriter, path, action string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		writeServerFileError(w, err, action)
		return false
	}
	if !info.IsDir() && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		WriteError(w, http.StatusBadRequest, "unsupported_file_type", "不能"+action+"设备、管道等特殊文件")
		return false
	}
	return true
}

func deleteServerFile(w http.ResponseWriter, r *http.Request) {
	path := serverFilePath(w, r.URL.Query().Get("path"), false)
	if path == "" || !serverFileCanRenameOrDelete(w, path, "删除") {
		return
	}
	if err := os.Remove(path); err != nil {
		writeServerFileError(w, err, "删除")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
