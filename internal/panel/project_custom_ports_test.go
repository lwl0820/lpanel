package panel

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"slices"
	"testing"
)

func TestProjectCustomPortsSave(t *testing.T) {
	a, _ := testApp(t)
	a.Command = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[9] == "config" {
			return []byte(`{"services":{}}`), nil
		}
		return nil, nil
	}
	login := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	const projectsPath = "/api/v1/projects"
	const body = `{"name":"自定义端口","compose":"services: {}"`

	var id string
	for _, save := range []struct {
		method, ports string
		want          []int
	}{
		{http.MethodPost, `,"custom_ports":[8080,65535,1,8080]`, []int{8080, 65535, 1}},
		{http.MethodPut, "", []int{8080, 65535, 1}},
		{http.MethodPut, `,"custom_ports":[]`, []int{}},
	} {
		path := projectsPath
		if save.method == http.MethodPut {
			path += "/" + id
		}
		w := call(a, save.method, path, body+save.ports+"}", cookie)
		var result struct {
			Project
			Operation Operation `json:"operation"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK ||
			result.CustomPorts == nil || !slices.Equal(result.CustomPorts, save.want) {
			t.Fatalf("保存自定义端口失败：%d %s %v", w.Code, w.Body, err)
		}
		id = result.ID
		waitProjectOperation(t, a, result.Operation.ID)
		detail := call(a, http.MethodGet, projectsPath+"/"+id, "", cookie)
		var project Project
		if err := json.Unmarshal(detail.Body.Bytes(), &project); err != nil || detail.Code != http.StatusOK ||
			project.CustomPorts == nil || !slices.Equal(project.CustomPorts, save.want) {
			t.Fatalf("详情中缺少已保存的自定义端口：%d %s %v", detail.Code, detail.Body, err)
		}
	}
	for _, ports := range []string{"[0]", "[65536]", "[8080.5]"} {
		w := call(a, http.MethodPut, projectsPath+"/"+id, body+`,"custom_ports":`+ports+"}", cookie)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("无效端口 %s 返回 %d：%s", ports, w.Code, w.Body)
		}
	}
}
