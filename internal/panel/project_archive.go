package panel

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/binary"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	projectArchiveManifest = "project.json"
	projectArchiveFiles    = "files/"
	zipUnixOwnership       = 0x7875
)

type projectArchiveMetadata struct {
	Name string `json:"name"`
}

func (a *App) exportProject(w http.ResponseWriter, r *http.Request) {
	archive, err := os.CreateTemp(filepath.Join(a.Root, ".tmp"), "project-export-")
	if err != nil {
		WriteError(w, 500, "project_export", fmt.Sprintf("无法创建项目压缩包：%v", err))
		return
	}
	defer os.Remove(archive.Name())
	defer archive.Close()

	id := r.PathValue("id")
	unlock := a.lockProject(id)
	p, err := a.project(r.Context(), id)
	if err != nil {
		unlock()
		if errors.Is(err, sql.ErrNoRows) {
			WriteError(w, 404, "not_found", "项目不存在")
		} else {
			WriteError(w, 500, "project_export", fmt.Sprintf("读取项目失败：%v", err))
		}
		return
	}
	if err = a.checkProjectDirectoryForRemoval(p.Directory); err == nil {
		err = writeProjectArchive(r.Context(), archive, p)
	}
	unlock()
	if err != nil {
		WriteError(w, 500, "project_export", fmt.Sprintf("无法导出项目：%v", err))
		return
	}
	info, err := archive.Stat()
	if err != nil {
		WriteError(w, 500, "project_export", fmt.Sprintf("无法读取项目压缩包：%v", err))
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": strings.ReplaceAll(p.Name, "/", "_") + ".zip"}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", info.ModTime(), archive)
}

func writeProjectArchive(ctx context.Context, dst io.Writer, p Project) error {
	root, err := os.OpenRoot(p.Directory)
	if err != nil {
		return err
	}
	defer root.Close()
	zw := zip.NewWriter(dst)
	manifest, err := zw.Create(projectArchiveManifest)
	if err != nil {
		return err
	}
	if err := json.MarshalWrite(manifest, projectArchiveMetadata{Name: p.Name}); err != nil {
		return err
	}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if (!info.IsDir() && !info.Mode().IsRegular()) || (name != "." && !validArchivePath(name)) {
			return fmt.Errorf("压缩包无法保留该文件或目录：%s", name)
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			// Info-ZIP Unix 扩展字段，按数值 ID 恢复文件所有者和组。
			header.Extra = binary.LittleEndian.AppendUint16(header.Extra, zipUnixOwnership)
			header.Extra = binary.LittleEndian.AppendUint16(header.Extra, 11)
			header.Extra = append(header.Extra, 1, 4)
			header.Extra = binary.LittleEndian.AppendUint32(header.Extra, stat.Uid)
			header.Extra = append(header.Extra, 4)
			header.Extra = binary.LittleEndian.AppendUint32(header.Extra, stat.Gid)
		}
		header.Name = projectArchiveFiles + name
		if info.IsDir() {
			header.Name += "/"
			if name == "." {
				header.Name = projectArchiveFiles
			}
		} else {
			header.Method = zip.Deflate
		}
		out, err := zw.CreateHeader(header)
		if err != nil || info.IsDir() {
			return err
		}
		file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		actual, err := file.Stat()
		if err != nil {
			return err
		}
		if !actual.Mode().IsRegular() {
			return fmt.Errorf("压缩包无法保留该文件：%s", name)
		}
		_, err = io.Copy(out, file)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

func validArchivePath(name string) bool {
	return name != "." && fs.ValidPath(name) && !strings.ContainsAny(name, "\\\x00")
}

func (a *App) importProject(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/zip" {
		WriteError(w, 415, "project_archive", "请选择 ZIP 项目压缩包")
		return
	}
	upload, err := os.CreateTemp(filepath.Join(a.Root, ".tmp"), "project-import-")
	if err != nil {
		WriteError(w, 500, "project_import", fmt.Sprintf("无法接收项目压缩包：%v", err))
		return
	}
	defer os.Remove(upload.Name())
	defer upload.Close()
	size, err := io.Copy(upload, r.Body)
	if err != nil {
		WriteError(w, 400, "project_archive", fmt.Sprintf("接收项目压缩包失败：%v", err))
		return
	}
	zr, err := zip.NewReader(upload, size)
	if err != nil {
		WriteError(w, 400, "project_archive", "无法读取项目 ZIP 压缩包")
		return
	}
	var manifest *zip.File
	for _, file := range zr.File {
		if file.Name != projectArchiveManifest {
			continue
		}
		if manifest != nil || !file.Mode().IsRegular() || file.UncompressedSize64 > 4096 {
			WriteError(w, 400, "project_archive", "项目压缩包中的项目信息无效")
			return
		}
		manifest = file
	}
	if manifest == nil {
		WriteError(w, 400, "project_archive", "项目压缩包中缺少项目信息")
		return
	}
	reader, err := manifest.Open()
	if err != nil {
		WriteError(w, 400, "project_archive", "无法读取压缩包中的项目信息")
		return
	}
	var metadata projectArchiveMetadata
	err = json.UnmarshalRead(reader, &metadata)
	reader.Close()
	if err != nil {
		WriteError(w, 400, "project_archive", "项目压缩包中的项目信息无效")
		return
	}

	ctx := r.Context()
	p := Project{ID: newProjectID(), Name: metadata.Name}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 || strings.ContainsRune(p.Name, 0) {
		WriteError(w, 400, "invalid_name", "压缩包中的项目名称无效")
		return
	}
	directory := p.Name
	if !validProjectDirectoryName(directory) || strings.TrimSpace(directory) != directory {
		directory = p.ID
		p.LegacyDirectory = true
	}
	p.Directory = a.projectDirectory(directory)
	a.projectsMu.Lock()
	defer a.projectsMu.Unlock()
	occupied, err := a.projectNameOrDirectoryInUse(ctx, p)
	if err != nil {
		WriteError(w, 500, "project_import", fmt.Sprintf("检查项目名称失败：%v", err))
		return
	}
	if occupied {
		WriteError(w, 409, "project_conflict", "该项目名称或目录已被使用")
		return
	}
	if _, err = os.Lstat(p.Directory); err == nil {
		WriteError(w, 409, "project_conflict", "该项目目录已存在，请先处理目标服务器上的同名目录")
		return
	}
	if err = a.checkProjectDirectory(p.Directory, true); err != nil {
		WriteError(w, 400, "project_directory", err.Error())
		return
	}
	if err = os.MkdirAll(filepath.Dir(p.Directory), 0700); err == nil {
		err = os.Mkdir(p.Directory, 0700)
	}
	if errors.Is(err, os.ErrExist) {
		WriteError(w, 409, "project_conflict", "该项目目录已存在，请先处理目标服务器上的同名目录")
		return
	}
	if err != nil {
		WriteError(w, 500, "project_import", fmt.Sprintf("无法创建项目目录：%v", err))
		return
	}
	fail := func(status int, code string, err error) {
		if cleanupErr := os.RemoveAll(p.Directory); cleanupErr != nil {
			// 还原后的目录可能没有写权限，先恢复本次导入目录的访问权限再清理。
			_ = filepath.WalkDir(p.Directory, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || !entry.IsDir() {
					return err
				}
				return os.Chmod(path, 0700)
			})
			if cleanupErr = os.RemoveAll(p.Directory); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("清理导入目录失败：%w", cleanupErr))
			}
		}
		WriteError(w, status, code, err.Error())
	}
	if err := unpackProjectArchive(ctx, zr, manifest, p.Directory); err != nil {
		fail(400, "project_archive", fmt.Errorf("无法还原项目文件：%w", err))
		return
	}
	if err := loadProjectConfig(&p); err != nil {
		fail(400, "compose_error", err)
		return
	}
	if _, err := a.scanProject(ctx, p, p.Directory); err != nil {
		fail(400, "compose_error", err)
		return
	}
	if err := a.storeProject(ctx, &p); err != nil {
		fail(500, "project_import", fmt.Errorf("保存项目失败：%w", err))
		return
	}
	a.writeProjectDeployment(w, p)
}

func unpackProjectArchive(ctx context.Context, zr *zip.Reader, manifest *zip.File, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	seen := make(map[string]bool)
	directories := make(map[string]*zip.File)
	for _, file := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file == manifest {
			continue
		}
		name, inProject := strings.CutPrefix(file.Name, projectArchiveFiles)
		mode := file.Mode()
		name = strings.TrimSuffix(name, "/")
		validName := validArchivePath(name)
		if file.Name == projectArchiveFiles && mode.IsDir() {
			name, validName = ".", true
		}
		if !inProject || !validName || seen[name] || (!mode.IsDir() && !mode.IsRegular()) {
			return fmt.Errorf("压缩包条目无效或重复：%s", file.Name)
		}
		seen[name] = true
		if mode.IsDir() {
			if err := root.MkdirAll(name, 0700); err != nil {
				return err
			}
			directories[name] = file
			continue
		}
		if err := root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		if err := errors.Join(copyErr, out.Close(), in.Close()); err != nil {
			return err
		}
		if err := restoreProjectArchivePermissions(root, name, file); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(directories))
	for name := range directories {
		if name != "." {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	slices.Reverse(names)
	if directories["."] != nil {
		names = append(names, ".")
	}
	for _, name := range names {
		if err := restoreProjectArchivePermissions(root, name, directories[name]); err != nil {
			return err
		}
	}
	return nil
}

func restoreProjectArchivePermissions(root *os.Root, name string, file *zip.File) error {
	extra := file.Extra
	for len(extra) >= 4 {
		tag, size := binary.LittleEndian.Uint16(extra), int(binary.LittleEndian.Uint16(extra[2:]))
		extra = extra[4:]
		if size > len(extra) {
			return fmt.Errorf("压缩包中的文件属性无效：%s", name)
		}
		if tag == zipUnixOwnership {
			if size != 11 || extra[0] != 1 || extra[1] != 4 || extra[6] != 4 {
				return fmt.Errorf("压缩包中的文件所有者无效：%s", name)
			}
			uid, gid := int(binary.LittleEndian.Uint32(extra[2:])), int(binary.LittleEndian.Uint32(extra[7:]))
			if err := root.Chown(name, uid, gid); err != nil {
				return fmt.Errorf("无法恢复文件所有者：%w", err)
			}
			break
		}
		extra = extra[size:]
	}
	mode := file.Mode()
	permissions := mode.Perm()
	if mode.IsDir() {
		permissions |= mode & (os.ModeSetgid | os.ModeSticky)
	}
	return root.Chmod(name, permissions)
}
