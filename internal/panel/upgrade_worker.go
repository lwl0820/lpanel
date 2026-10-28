package panel

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) prepareUpgrade(ctx context.Context, state UpgradeState) (string, error) {
	if !releaseVersion.MatchString(state.Latest) {
		return "", fmt.Errorf("发行版本无效")
	}
	temp, err := os.MkdirTemp(filepath.Join(a.Root, ".tmp"), "upgrade-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)
	client := &http.Client{Timeout: 10 * time.Minute}
	var expected string
	for i, url := range []string{state.ChecksumURL, state.AssetURL} {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return "", err
		}
		response, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("下载发行文件失败：%w", err)
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return "", fmt.Errorf("下载发行文件失败（HTTP %d）", response.StatusCode)
		}
		if i == 0 {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if readErr != nil {
				return "", readErr
			}
			fields := strings.Fields(string(data))
			if len(fields) < 1 {
				return "", fmt.Errorf("校验文件为空")
			}
			decoded, decodeErr := hex.DecodeString(fields[0])
			if decodeErr != nil || len(decoded) != 32 {
				return "", fmt.Errorf("校验文件格式错误")
			}
			expected = strings.ToLower(fields[0])
			continue
		}
		file, err := os.Create(filepath.Join(temp, "release.tar.gz"))
		if err != nil {
			response.Body.Close()
			return "", err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(file, hash), response.Body)
		response.Body.Close()
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected {
			return "", fmt.Errorf("发行包校验失败")
		}
	}
	stage := filepath.Join(temp, "release")
	if err = os.Mkdir(stage, 0755); err != nil {
		return "", err
	}
	file, err := os.Open(filepath.Join(temp, "release.tar.gz"))
	if err != nil {
		return "", err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("发行包不是有效 gzip：%w", err)
	}
	defer gzipReader.Close()
	archive := tar.NewReader(gzipReader)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name := filepath.Clean(header.Name)
		if name == "." {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("发行包包含无效路径")
		}
		first := strings.Split(name, string(filepath.Separator))[0]
		if first != "lpanel" && first != "web" && first != "deploy" {
			return "", fmt.Errorf("发行包包含未知内容：%s", first)
		}
		target := filepath.Join(stage, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return "", err
			}
			mode := os.FileMode(0644)
			if name == "lpanel" || strings.HasPrefix(name, "deploy/") && strings.HasSuffix(name, ".sh") {
				mode = 0755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(out, archive)
			closeErr := out.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		default:
			return "", fmt.Errorf("发行包包含不支持的文件类型")
		}
	}
	for _, required := range []string{"lpanel", "web/index.html"} {
		info, err := os.Stat(filepath.Join(stage, required))
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("发行包缺少 %s", required)
		}
	}
	if err = os.MkdirAll(filepath.Join(a.Root, "releases"), 0755); err != nil {
		return "", err
	}
	target := filepath.Join(a.Root, "releases", state.Latest)
	if _, err = os.Stat(target); err == nil {
		current, _ := filepath.EvalSymlinks(filepath.Join(a.Root, "current"))
		if current == target {
			return "", fmt.Errorf("目标版本已在运行")
		}
		if err = os.RemoveAll(target); err != nil {
			return "", err
		}
	}
	if err = os.Rename(stage, target); err != nil {
		return "", err
	}
	return target, nil
}

func UpgradeWorker(ctx context.Context, root, version, operationID, releaseDir string) error {
	app, err := Open(root, version)
	if err != nil {
		return err
	}
	defer app.Close()
	return app.applyUpgrade(ctx, operationID, releaseDir)
}
func (a *App) applyUpgrade(ctx context.Context, id, releaseDir string) (result error) {
	defer func() {
		if result != nil {
			a.finishUpgrade(id, "failed", "", result.Error())
		} else {
			a.finishUpgrade(id, "success", "版本升级完成", "")
		}
	}()
	releaseDir, err := filepath.Abs(releaseDir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(filepath.Join(a.Root, "releases"), releaseDir)
	if err != nil || strings.Contains(relative, string(filepath.Separator)) || !releaseVersion.MatchString(relative) {
		return fmt.Errorf("升级版本目录无效")
	}
	if _, err = os.Stat(filepath.Join(releaseDir, "lpanel")); err != nil {
		return err
	}
	current := filepath.Join(a.Root, "current")
	previous, err := os.Readlink(current)
	if err != nil {
		return fmt.Errorf("读取当前版本失败：%w", err)
	}
	if err = a.switchRelease(releaseDir); err != nil {
		return err
	}
	_, restartErr := a.Command(ctx, "systemctl", "restart", "lpanel.service")
	if restartErr == nil {
		restartErr = a.waitUpgradeHealth(ctx, relative)
	}
	if restartErr != nil {
		if err = a.switchRelease(previous); err != nil {
			return fmt.Errorf("升级失败：%v；恢复旧版本指针失败：%w", restartErr, err)
		}
		if _, err = a.Command(ctx, "systemctl", "restart", "lpanel.service"); err != nil {
			return fmt.Errorf("升级失败：%v；重启旧版本失败：%w", restartErr, err)
		}
		return fmt.Errorf("升级失败，已恢复上一版程序：%w", restartErr)
	}
	return nil
}
func (a *App) switchRelease(target string) error {
	candidate := filepath.Join(a.Root, ".tmp", "current-next")
	_ = os.Remove(candidate)
	if err := os.Symlink(target, candidate); err != nil {
		return err
	}
	return os.Rename(candidate, filepath.Join(a.Root, "current"))
}
func (a *App) waitUpgradeHealth(ctx context.Context, version string) error {
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, "GET", "http://"+a.Config.Listen+"/api/v1/health", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		if err == nil {
			var health struct {
				Status  string `json:"status"`
				Version string `json:"version"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&health)
			response.Body.Close()
			if response.StatusCode == 200 && decodeErr == nil && health.Status == "ok" && health.Version == version {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("新版启动后健康检查未通过")
		case <-ticker.C:
		}
	}
}
