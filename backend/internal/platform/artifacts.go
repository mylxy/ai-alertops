package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runtimeMetadata 保留各固定克隆的初始基线及项目指导文件摘要，便于重现执行配置。
func runtimeMetadata(ctx context.Context, workspace string, repos []Row) (Row, error) {
	bases := []Row{}
	for _, repo := range repos {
		path := filepath.Join(workspace, repo.S("relative_path"))
		head, e := gitProbe(ctx, path, "rev-parse", "--verify", "HEAD")
		revision := "UNBORN"
		if e == nil {
			revision = strings.TrimSpace(string(head))
		} else {
			// 空仓库没有提交，仍保留显式状态；定位后的 fetch/master 门槛会阻止错误建分支。
			refs, err := gitProbe(ctx, path, "show-ref")
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || len(refs) > 0 {
				return nil, e
			}
		}
		bases = append(bases, Row{"repo_code": repo.S("repo_code"), "initial_revision": revision, "configured_git_url": repo.S("git_url")})
	}
	guidance := []Row{}
	err := filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", ".venv", "target", "build", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.Name() != "SKILL.md" && entry.Name() != "AGENTS.md" {
			return nil
		}
		data, e := readBoundedFile(path, 4<<20)
		if e != nil {
			return e
		}
		hash := sha256.Sum256(data)
		rel, e := filepath.Rel(workspace, path)
		if e != nil {
			return e
		}
		guidance = append(guidance, Row{"relative_path": rel, "sha256": hex.EncodeToString(hash[:])})
		return nil
	})
	return Row{"repos": bases, "guidance": guidance}, err
}
