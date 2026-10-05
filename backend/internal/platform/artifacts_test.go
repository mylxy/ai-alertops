package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestUntrustedArtifactFiles 验证容器产物不能让宿主跟随链接、阻塞 FIFO 或读取无限数据。
func TestUntrustedArtifactFiles(t *testing.T) {
	// 准备：全部文件位于本次测试临时目录。
	root := t.TempDir()
	sentinel := filepath.Join(root, "sentinel")
	if e := os.WriteFile(sentinel, []byte("保留内容"), 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(root, "link")
	if e := os.Symlink(sentinel, link); e != nil {
		t.Fatal(e)
	}
	fifo := filepath.Join(root, "pipe")
	if e := syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	// 执行和验证：特殊文件立即失败；普通文件遵循长度限制。
	started := time.Now()
	for _, path := range []string{link, fifo, root} {
		if _, e := readBoundedFile(path, 100); e == nil {
			t.Fatal("接受了非普通产物", path)
		}
	}
	if time.Since(started) > time.Second {
		t.Fatal("特殊文件阻塞读取")
	}
	if _, e := readBoundedFile(sentinel, 1); e == nil {
		t.Fatal("忽略长度限制")
	}
	data, e := os.ReadFile(sentinel)
	if e != nil || string(data) != "保留内容" {
		t.Fatal("越界修改哨兵")
	}
}

// TestHostGitRejectsExecutableConfig 验证健康探测不运行任务仓库内的配置命令。
func TestHostGitRejectsExecutableConfig(t *testing.T) {
	// 准备：独立空仓库及仅写入临时哨兵的 fsmonitor 配置。
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command("git", "init", root).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	sentinel := filepath.Join(root, "unexpected")
	if out, e := exec.Command("git", "-C", root, "config", "core.fsmonitor", "touch "+sentinel).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	// 执行：宿主拒绝超出只读探测允许范围的配置。
	if _, e := gitProbe(context.Background(), root, "status", "--porcelain"); e == nil {
		t.Fatal("接受了可执行扩展")
	}
	// 验证：命令未运行，原现场仍保留。
	if _, e := os.Stat(sentinel); !os.IsNotExist(e) {
		t.Fatal("宿主执行了容器仓库配置")
	}
}
