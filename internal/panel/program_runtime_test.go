package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProgramLogTailAppendAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	var cursor programLogCursor
	check := func(wantEvent, wantText string) {
		t.Helper()
		event, text, err := cursor.read(path)
		if err != nil || event != wantEvent || text != wantText {
			t.Fatalf("读取日志得到 %s, %q, %v", event, text, err)
		}
	}
	check("", "")
	last := strings.Repeat("最近的输出\n", 200)
	if err := os.WriteFile(path, []byte("过早的输出\n"+last), 0600); err != nil {
		t.Fatal(err)
	}
	check("snapshot", last)
	check("", "")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("新行\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	check("append", "新行\n")
	// 两次写入跨过同一个中文字符时，浏览器仍收到完整原文。
	for i, chunk := range [][]byte{[]byte("字")[:1], []byte("字")[1:]} {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(chunk); err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			check("", "")
		} else {
			check("append", "字")
		}
	}
	if err = os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("轮转后的第一行\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check("snapshot", "轮转后的第一行\n")
}
