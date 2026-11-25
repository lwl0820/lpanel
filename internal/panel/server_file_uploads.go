package panel

import (
	"crypto/rand"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	serverFileChunkSize  = 512 << 10
	serverFileUploadIdle = 30 * time.Minute
	serverFileUploadData = "data"
)

type serverFileUploads struct {
	mu    sync.Mutex
	items map[string]*serverFileUpload
}

type serverFileUpload struct {
	mu        sync.Mutex
	dir       *os.File
	staging   *os.File
	file      *os.File
	session   string
	id        string
	target    string
	temporary string
	size      int64
	received  int64
	expires   time.Time
	timer     *time.Timer
}

func (a *App) registerServerFileUploads() {
	uploads := &serverFileUploads{items: make(map[string]*serverFileUpload)}
	const prefix = "/api/v1/server/files/uploads"
	for pattern, handler := range map[string]http.HandlerFunc{
		"POST " + prefix:                    uploads.create,
		"PUT " + prefix + "/{id}":           uploads.appendChunk,
		"POST " + prefix + "/{id}/complete": uploads.complete,
		"DELETE " + prefix + "/{id}":        uploads.abort,
	} {
		a.Mux.Handle(pattern, a.RequireAuth(handler))
	}
}

func (uploads *serverFileUploads) create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
		Size *int64 `json:"size"`
	}
	if !readServerFileJSON(w, r, &input) {
		return
	}
	path := serverFilePath(w, input.Path, false)
	if path == "" {
		return
	}
	if input.Size == nil || *input.Size < 0 {
		WriteError(w, http.StatusBadRequest, "invalid_upload_size", "文件大小必须为非负整数")
		return
	}
	parent := filepath.Dir(path)
	dirFD, err := unix.Open(parent, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		writeServerFileError(w, err, "开始上传")
		return
	}
	dir := os.NewFile(uintptr(dirFD), parent)
	target := filepath.Base(path)
	var info unix.Stat_t
	err = unix.Fstatat(dirFD, target, &info, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		err = os.ErrExist
	}
	if !errors.Is(err, os.ErrNotExist) {
		_ = dir.Close()
		writeServerFileError(w, err, "开始上传")
		return
	}
	id := rand.Text()
	temporary := ".lpanel-upload-" + id
	// 暂存目录与目标同属一个文件系统，文件沿用整文件上传的创建权限。
	if err := unix.Mkdirat(dirFD, temporary, 0700); err != nil {
		_ = dir.Close()
		writeServerFileError(w, err, "开始上传")
		return
	}
	stagingFD, err := unix.Openat(dirFD, temporary, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		err = errors.Join(err, unix.Unlinkat(dirFD, temporary, unix.AT_REMOVEDIR))
		_ = dir.Close()
		writeServerFileError(w, err, "开始上传")
		return
	}
	staging := os.NewFile(uintptr(stagingFD), temporary)
	fd, err := unix.Openat(stagingFD, serverFileUploadData, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0666)
	if err != nil {
		_ = staging.Close()
		err = errors.Join(err, unix.Unlinkat(dirFD, temporary, unix.AT_REMOVEDIR))
		_ = dir.Close()
		writeServerFileError(w, err, "开始上传")
		return
	}
	cookie, _ := r.Cookie("lpanel_session")
	upload := &serverFileUpload{
		dir: dir, staging: staging, file: os.NewFile(uintptr(fd), temporary), session: cookie.Value,
		id: id, target: target, temporary: temporary, size: *input.Size,
		expires: time.Now().Add(serverFileUploadIdle),
	}
	upload.timer = time.AfterFunc(serverFileUploadIdle, func() {
		upload.mu.Lock()
		defer upload.mu.Unlock()
		if upload.dir == nil {
			return
		}
		if remaining := time.Until(upload.expires); remaining > 0 {
			upload.timer.Reset(remaining)
			return
		}
		if err := uploads.discard(upload); err != nil {
			log.Printf("清理过期上传失败：%v", err)
		}
	})
	uploads.mu.Lock()
	uploads.items[id] = upload
	uploads.mu.Unlock()
	WriteJSON(w, http.StatusCreated, struct {
		ID        string `json:"id"`
		ChunkSize int    `json:"chunk_size"`
	}{ID: id, ChunkSize: serverFileChunkSize})
}

// 返回仍有效的上传，并持有该上传的锁。
func (uploads *serverFileUploads) lock(w http.ResponseWriter, r *http.Request) *serverFileUpload {
	uploads.mu.Lock()
	upload := uploads.items[r.PathValue("id")]
	uploads.mu.Unlock()
	cookie, _ := r.Cookie("lpanel_session")
	if upload != nil && upload.session == cookie.Value {
		upload.mu.Lock()
		if upload.dir != nil {
			return upload
		}
		upload.mu.Unlock()
	}
	WriteError(w, http.StatusNotFound, "upload_not_found", "上传已结束或已过期，请重新上传")
	return nil
}

func (uploads *serverFileUploads) discard(upload *serverFileUpload) error {
	upload.timer.Stop()
	uploads.mu.Lock()
	delete(uploads.items, upload.id)
	uploads.mu.Unlock()

	var closeErr error
	if upload.file != nil {
		closeErr = upload.file.Close()
		upload.file = nil
	}
	dataErr := unix.Unlinkat(int(upload.staging.Fd()), serverFileUploadData, 0)
	if errors.Is(dataErr, os.ErrNotExist) {
		dataErr = nil
	}
	dirErr := unix.Unlinkat(int(upload.dir.Fd()), upload.temporary, unix.AT_REMOVEDIR)
	if errors.Is(dirErr, os.ErrNotExist) {
		dirErr = nil
	}
	_ = upload.staging.Close()
	_ = upload.dir.Close()
	upload.dir = nil
	return errors.Join(closeErr, dataErr, dirErr)
}

func (uploads *serverFileUploads) appendChunk(w http.ResponseWriter, r *http.Request) {
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		WriteError(w, http.StatusBadRequest, "invalid_upload_offset", "上传偏移无效")
		return
	}
	upload := uploads.lock(w, r)
	if upload == nil {
		return
	}
	defer upload.mu.Unlock()
	if offset != upload.received {
		WriteError(w, http.StatusConflict, "upload_offset_mismatch", "上传进度不一致，请重新上传")
		return
	}
	limit := min(int64(serverFileChunkSize), upload.size-upload.received)
	chunk, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil || len(chunk) == 0 {
		if cleanupErr := uploads.discard(upload); cleanupErr != nil {
			log.Printf("清理失败的上传：%v", cleanupErr)
		}
		_, tooLarge := errors.AsType[*http.MaxBytesError](err)
		switch {
		case tooLarge:
			WriteError(w, http.StatusRequestEntityTooLarge, "upload_chunk_too_large", "分片超过允许的大小")
		case err != nil:
			WriteError(w, http.StatusBadRequest, "upload_chunk_read_failed", "分片传输中断，请重新上传")
		default:
			WriteError(w, http.StatusBadRequest, "empty_upload_chunk", "上传分片不能为空")
		}
		return
	}

	if _, err := upload.file.Write(chunk); err != nil {
		writeServerFileError(w, errors.Join(err, uploads.discard(upload)), "写入上传文件")
		return
	}
	upload.received += int64(len(chunk))
	upload.expires = time.Now().Add(serverFileUploadIdle)
	WriteJSON(w, http.StatusOK, map[string]int64{"received": upload.received})
}

func (uploads *serverFileUploads) complete(w http.ResponseWriter, r *http.Request) {
	upload := uploads.lock(w, r)
	if upload == nil {
		return
	}
	defer upload.mu.Unlock()
	if upload.received != upload.size {
		WriteError(w, http.StatusConflict, "upload_incomplete", "文件尚未传输完整")
		return
	}
	err := upload.file.Close()
	upload.file = nil
	if err == nil {
		err = unix.Renameat2(int(upload.staging.Fd()), serverFileUploadData, int(upload.dir.Fd()), upload.target, unix.RENAME_NOREPLACE)
	}
	cleanupErr := uploads.discard(upload)
	if err != nil {
		writeServerFileError(w, errors.Join(err, cleanupErr), "保存上传文件")
		return
	}
	if cleanupErr != nil {
		log.Printf("清理已完成的上传：%v", cleanupErr)
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (uploads *serverFileUploads) abort(w http.ResponseWriter, r *http.Request) {
	upload := uploads.lock(w, r)
	if upload == nil {
		return
	}
	defer upload.mu.Unlock()
	if err := uploads.discard(upload); err != nil {
		writeServerFileError(w, err, "清理上传文件")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
