package panel

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestImageRoutes(t *testing.T) {
	a, _ := testApp(t)
	const path = "/api/v1/images"
	const taggedID = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const untaggedID = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	var commands [][]string
	var fail bool
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, append([]string{name}, args...))
		if fail {
			return []byte("Error response from daemon: conflict: image is being used by stopped container"), errors.New("命令失败")
		}
		if slices.Equal(args, []string{"image", "ls", "--all", "--no-trunc", "--format", "json"}) {
			return []byte(`{"ID":"` + taggedID + `","Repository":"example/web","Tag":"latest","Size":"12MB"}
{"ID":"` + taggedID + `","Repository":"localhost:5000/web","Tag":"v1","Size":"12MB"}
{"ID":"` + untaggedID + `","Repository":"<none>","Tag":"<none>","Size":"8MB"}
`), nil
		}
		return nil, nil
	}
	for _, tt := range []struct{ method, path string }{
		{"GET", path}, {"DELETE", path + "/" + taggedID}, {"POST", path + "/prune"},
	} {
		if w := call(a, tt.method, tt.path, "", nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("镜像接口 %s %s 未校验会话：%d", tt.method, tt.path, w.Code)
		}
	}
	if len(commands) != 0 {
		t.Fatalf("未登录时执行了命令：%v", commands)
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	w := call(a, "GET", path, "", cookie)
	var images []localImage
	if err := json.Unmarshal(w.Body.Bytes(), &images); err != nil || w.Code != http.StatusOK ||
		len(images) != 2 || images[0].ID != taggedID || images[0].Size != "12MB" ||
		!slices.Equal(images[0].Tags, []string{"example/web:latest", "localhost:5000/web:v1"}) ||
		images[1].ID != untaggedID || images[1].Tags == nil || len(images[1].Tags) != 0 {
		t.Fatalf("镜像分组或字段错误：%d %s", w.Code, w.Body.String())
	}

	w = call(a, "DELETE", path+"/"+untaggedID, "", cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"message"`) {
		t.Fatalf("删除镜像响应错误：%d %s", w.Code, w.Body.String())
	}
	w = call(a, "POST", path+"/prune", "", cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"message"`) {
		t.Fatalf("清理镜像响应错误：%d %s", w.Code, w.Body.String())
	}
	if len(commands) != 3 ||
		!slices.Equal(commands[1], []string{"docker", "image", "rm", "--no-prune", "--", untaggedID}) ||
		!slices.Equal(commands[2], []string{"docker", "image", "prune", "--all", "--force"}) {
		t.Fatalf("删除或清理镜像命令错误：%v", commands)
	}
	fail = true
	w = call(a, "DELETE", path+"/"+taggedID, "", cookie)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "仍被容器使用或存在多个标签") ||
		!strings.Contains(w.Body.String(), "image is being used by stopped container") {
		t.Fatalf("镜像删除失败原因未返回：%d %s", w.Code, w.Body.String())
	}
}
