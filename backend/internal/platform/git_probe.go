package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitProbe 仅供宿主读取仓库；容器可改 Git 配置，因此先拒绝执行扩展和共享元数据。
func gitProbe(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gitDir := filepath.Join(path, ".git")
	if _, err := canonicalDirectory(gitDir); err != nil {
		return nil, errors.New("Git 元数据必须为独立实际目录")
	}
	for _, name := range []string{"commondir", "config.worktree", "objects/info/alternates"} {
		if _, err := os.Lstat(filepath.Join(gitDir, name)); err == nil || !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("宿主检查不支持共享 Git 元数据或工作区扩展")
		}
	}
	if _, err := os.Lstat(filepath.Join(path, ".gitmodules")); err == nil || !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("修复槽位需使用独立仓库；子模块须单独登记为仓库")
	}
	config, err := readBoundedFile(filepath.Join(gitDir, "config"), 1<<20)
	if err != nil {
		return nil, err
	}
	// 在仓库之外解析快照且关闭 include，不允许用户配置和环境改变探测行为。
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"}
	parser := exec.CommandContext(ctx, "git", "config", "--file", "-", "--no-includes", "--null", "--name-only", "--list")
	parser.Env = env
	parser.Stdin = bytes.NewReader(config)
	keys, err := parser.Output()
	if err != nil {
		return nil, errors.New("Git 配置解析失败")
	}
	safe := map[string]bool{"core.repositoryformatversion": true, "core.filemode": true, "core.bare": true, "core.logallrefupdates": true, "core.ignorecase": true, "core.precomposeunicode": true, "core.symlinks": true, "core.autocrlf": true, "core.safecrlf": true, "core.eol": true, "user.name": true, "user.email": true}
	for _, key := range strings.Split(strings.TrimRight(string(keys), "\x00"), "\x00") {
		key = strings.ToLower(key)
		remote := strings.HasPrefix(key, "remote.") && (strings.HasSuffix(key, ".url") || strings.HasSuffix(key, ".fetch"))
		branch := strings.HasPrefix(key, "branch.") && (strings.HasSuffix(key, ".remote") || strings.HasSuffix(key, ".merge"))
		if !safe[key] && !remote && !branch {
			return nil, errors.New("Git 配置包含宿主检查未允许的扩展，保留现场")
		}
	}
	command := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", path}, args...)...)
	command.Env = env
	return command.Output()
}
