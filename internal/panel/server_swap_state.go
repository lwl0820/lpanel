package panel

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type swapIdentity struct {
	device uint64
	inode  uint64
	path   string
}

type swapEntry struct {
	Path      string `json:"path"`
	Type      string `json:"type"`
	SizeBytes *int64 `json:"size_bytes"`
	UsedBytes int64  `json:"used_bytes"`
	Active    bool   `json:"active"`
	Managed   bool   `json:"managed"`

	identity swapIdentity
}

type swapState struct {
	TotalBytes int64       `json:"total_bytes"`
	UsedBytes  int64       `json:"used_bytes"`
	Entries    []swapEntry `json:"entries"`

	fstab string
}

var decodeSwapPath = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
var encodeSwapPath = strings.NewReplacer(`\`, `\134`, " ", `\040`, "\t", `\011`, "\n", `\012`)

func inspectSwapPath(source string, activeType string) swapEntry {
	entry := swapEntry{Path: source, Type: "unknown", identity: swapIdentity{path: source}}
	if activeType == "partition" {
		entry.Type = "partition"
	}

	path := source
	if !filepath.IsAbs(path) {
		tag, value, ok := strings.Cut(source, "=")
		if !ok || value == "" || value == "." || value == ".." || filepath.Base(value) != value {
			return entry
		}
		switch tag {
		case "UUID", "LABEL", "PARTUUID", "PARTLABEL":
			path = filepath.Join("/dev/disk", "by-"+strings.ToLower(tag), value)
		default:
			return entry
		}
	}

	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return entry
	}
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFBLK:
		entry.identity = swapIdentity{device: uint64(stat.Rdev)}
		entry.Type = "partition"
		if activeType == "" {
			sizePath := fmt.Sprintf("/sys/dev/block/%d:%d/size", unix.Major(uint64(stat.Rdev)), unix.Minor(uint64(stat.Rdev)))
			if raw, err := os.ReadFile(sizePath); err == nil {
				if sectors, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); err == nil && sectors > 0 {
					entry.SizeBytes = new(sectors * 512)
				}
			}
		}
	case unix.S_IFREG:
		entry.identity = swapIdentity{device: uint64(stat.Dev), inode: stat.Ino}
		if activeType != "partition" {
			entry.SizeBytes = new(stat.Size)
			actual, err := filepath.EvalSymlinks(path)
			if err == nil && filepath.IsAbs(source) && stat.Nlink == 1 {
				entry.Path = actual
				// 开机配置和归属记录可能指向被替换的普通文件，核对交换文件头。
				if activeType == "file" {
					entry.Type = "file"
				} else if f, err := os.Open(path); err == nil {
					var magic [10]byte
					_, err := f.ReadAt(magic[:], int64(os.Getpagesize()-len(magic)))
					_ = f.Close()
					if err == nil && string(magic[:]) == "SWAPSPACE2" {
						entry.Type = "file"
					}
				}
			}
		}
	}
	return entry
}

func (a *App) readActiveSwap() ([]swapEntry, error) {
	raw, err := os.ReadFile(a.swapProcPath)
	if err != nil {
		return nil, fmt.Errorf("无法读取已启用的交换空间：%w", err)
	}
	header, lines, _ := strings.Cut(string(raw), "\n")
	if fields := strings.Fields(header); len(fields) == 0 || fields[0] != "Filename" {
		return nil, errors.New("无法识别已启用的交换空间列表")
	}
	var entries []swapEntry
	for line := range strings.SplitSeq(lines, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) == 6 && fields[1] == "(deleted)" {
			fields[0] += " (deleted)"
			fields = slices.Delete(fields, 1, 2)
		}
		if len(fields) != 5 {
			return nil, errors.New("无法识别已启用的交换空间条目")
		}
		size, sizeErr := strconv.ParseInt(fields[2], 10, 64)
		used, usedErr := strconv.ParseInt(fields[3], 10, 64)
		if sizeErr != nil || usedErr != nil || size < 0 || used < 0 {
			return nil, errors.New("无法识别交换空间容量")
		}
		entry := inspectSwapPath(decodeSwapPath.Replace(fields[0]), fields[1])
		entry.Active = true
		entry.SizeBytes = new(size * 1024)
		entry.UsedBytes = used * 1024
		entries = append(entries, entry)
	}
	return entries, nil
}

func (a *App) readServerSwap(ctx context.Context) (swapState, error) {
	active, err := a.readActiveSwap()
	if err != nil {
		return swapState{}, err
	}
	fstab, err := os.ReadFile(a.swapFstabPath)
	if err != nil {
		return swapState{}, fmt.Errorf("无法读取开机交换配置：%w", err)
	}
	rows, err := a.DB.QueryContext(ctx, "SELECT path FROM server_swap_files")
	if err != nil {
		return swapState{}, err
	}
	defer rows.Close()
	managed := make(map[string]bool)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return swapState{}, err
		}
		managed[path] = true
	}
	if err := rows.Err(); err != nil {
		return swapState{}, err
	}

	entries := make(map[swapIdentity]swapEntry)
	for _, entry := range active {
		entries[entry.identity] = entry
	}
	for line := range strings.SplitSeq(string(fstab), "\n") {
		path, _ := swapFstabEntry(line)
		if path == "" {
			continue
		}
		entry := inspectSwapPath(path, "")
		if current, ok := entries[entry.identity]; ok {
			switch {
			case current.Type == "partition" || entry.Type == "partition":
				current.Type = "partition"
			case current.Path != entry.Path:
				// 别名可能另有开机设置，整个交换项保持只读。
				current.Type = "unknown"
			}
			entry = current
		}
		entries[entry.identity] = entry
	}
	for path := range managed {
		entry := inspectSwapPath(path, "")
		if _, ok := entries[entry.identity]; !ok {
			entries[entry.identity] = entry
		}
	}

	state := swapState{fstab: string(fstab), Entries: make([]swapEntry, 0, len(entries))}
	for entry := range maps.Values(entries) {
		entry.Managed = managed[entry.Path]
		if entry.Active {
			state.TotalBytes += *entry.SizeBytes
			state.UsedBytes += entry.UsedBytes
		}
		state.Entries = append(state.Entries, entry)
	}
	slices.SortFunc(state.Entries, func(a, b swapEntry) int { return strings.Compare(a.Path, b.Path) })
	return state, nil
}

func swapFstabEntry(line string) (path, options string) {
	fields := strings.Fields(line)
	if len(fields) < 3 || strings.HasPrefix(fields[0], "#") || fields[2] != "swap" {
		return "", ""
	}
	if len(fields) >= 4 {
		options = fields[3]
	}
	return decodeSwapPath.Replace(fields[0]), options
}

func swapFstabWith(fstab, path string, enabled bool) string {
	var updated strings.Builder
	found := false
	target, _ := os.Stat(path)
	for line := range strings.SplitAfterSeq(fstab, "\n") {
		source, options := swapFstabEntry(line)
		if source != path {
			var alias os.FileInfo
			if target != nil && filepath.IsAbs(source) {
				alias, _ = os.Stat(source)
			}
			if alias == nil || !os.SameFile(target, alias) {
				updated.WriteString(line)
				continue
			}
		}
		if enabled {
			found = true
			parts := strings.Split(options, ",")
			originalCount := len(parts)
			parts = slices.DeleteFunc(parts, func(option string) bool { return option == "noauto" })
			if len(parts) != originalCount {
				if len(parts) == 0 {
					parts = []string{"defaults"}
				}
				// 从第四列起替换选项，保留其他字段和行尾注释。
				offset := 0
				for index, field := range strings.Fields(line) {
					offset += strings.Index(line[offset:], field)
					if index == 3 {
						line = line[:offset] + strings.Join(parts, ",") + line[offset+len(options):]
						break
					}
					offset += len(field)
				}
			}
			updated.WriteString(line)
		}
	}
	if enabled && !found {
		if updated.Len() != 0 && !strings.HasSuffix(updated.String(), "\n") {
			updated.WriteByte('\n')
		}
		fmt.Fprintf(&updated, "%s none swap defaults 0 0\n", encodeSwapPath.Replace(path))
	}
	return updated.String()
}

func (a *App) writeSwapFstab(previous, updated string) error {
	if previous == updated {
		return nil
	}
	path, err := filepath.EvalSymlinks(a.swapFstabPath)
	if err != nil {
		return fmt.Errorf("无法定位开机交换配置：%w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("无法读取开机交换配置属性：%w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("无法打开开机交换配置目录：%w", err)
	}
	defer dir.Close()
	f, err := os.CreateTemp(dir.Name(), ".lpanel-fstab-")
	if err != nil {
		return fmt.Errorf("无法创建开机交换配置临时文件：%w", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	owner := info.Sys().(*syscall.Stat_t)
	if err := f.Chown(int(owner.Uid), int(owner.Gid)); err != nil {
		return fmt.Errorf("无法保留开机交换配置所有权：%w", err)
	}
	if err := f.Chmod(info.Mode()); err != nil {
		return fmt.Errorf("无法保留开机交换配置权限：%w", err)
	}
	if _, err := f.WriteString(updated); err != nil {
		return fmt.Errorf("无法写入开机交换配置：%w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("无法同步开机交换配置：%w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("无法关闭开机交换配置：%w", err)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("无法读取开机交换配置：%w", err)
	}
	if string(current) != previous {
		return errors.New("开机交换配置已变更，请刷新后重试")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("无法替换开机交换配置：%w", err)
	}
	// 替换已生效，尽量同步目录项；同步失败时原有或新配置仍各自完整。
	_ = dir.Sync()
	return nil
}
