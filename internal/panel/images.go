package panel

import (
	"bytes"
	"cmp"
	"encoding/json/v2"
	"net/http"
	"strings"
)

type localImage struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags"`
	Size string   `json:"size"`
}

func (a *App) registerImages() {
	a.Mux.Handle("GET /api/v1/images", a.RequireAuth(http.HandlerFunc(a.listImages)))
	a.Mux.Handle("DELETE /api/v1/images/{id}", a.RequireAuth(http.HandlerFunc(a.deleteImage)))
	a.Mux.Handle("POST /api/v1/images/prune", a.RequireAuth(http.HandlerFunc(a.pruneImages)))
}

func (a *App) listImages(w http.ResponseWriter, r *http.Request) {
	out, err := a.Command(r.Context(), "docker", "image", "ls", "--all", "--no-trunc", "--format", "json")
	if err != nil {
		WriteError(w, 502, "docker_error", "读取镜像列表失败："+cmp.Or(strings.TrimSpace(string(out)), err.Error()))
		return
	}
	items := []localImage{}
	indices := map[string]int{}
	for line := range bytes.SplitSeq(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row struct {
			ID         string `json:"ID"`
			Repository string `json:"Repository"`
			Tag        string `json:"Tag"`
			Size       string `json:"Size"`
		}
		if err := json.Unmarshal(line, &row); err != nil || row.ID == "" {
			WriteError(w, 502, "docker_error", "无法读取 Docker 返回的镜像列表")
			return
		}
		index, exists := indices[row.ID]
		if !exists {
			index = len(items)
			indices[row.ID] = index
			items = append(items, localImage{ID: row.ID, Tags: []string{}, Size: row.Size})
		}
		if row.Repository != "<none>" && row.Tag != "<none>" {
			items[index].Tags = append(items[index].Tags, row.Repository+":"+row.Tag)
		}
	}
	WriteJSON(w, 200, items)
}

func (a *App) deleteImage(w http.ResponseWriter, r *http.Request) {
	out, err := a.Command(r.Context(), "docker", "image", "rm", "--no-prune", "--", r.PathValue("id"))
	if err != nil {
		detail := cmp.Or(strings.TrimSpace(string(out)), err.Error())
		message := "删除镜像失败。"
		if strings.Contains(detail, "conflict:") {
			message += "请检查该镜像是否仍被容器使用或存在多个标签，处理相关容器或标签后再试。"
		}
		WriteError(w, 502, "docker_error", message+detail)
		return
	}
	WriteJSON(w, 200, map[string]string{"message": "镜像已删除"})
}

func (a *App) pruneImages(w http.ResponseWriter, r *http.Request) {
	out, err := a.Command(r.Context(), "docker", "image", "prune", "--all", "--force")
	if err != nil {
		WriteError(w, 502, "docker_error", "清理镜像失败："+cmp.Or(strings.TrimSpace(string(out)), err.Error()))
		return
	}
	WriteJSON(w, 200, map[string]string{"message": "未使用的镜像已清理"})
}
