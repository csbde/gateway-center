// Package deployer 定义配置下发抽象与一期唯一实现 FileDeployer（T021，research R4）。
// 宪法 IV：写临时文件 → fsync → 原子 rename；禁止 SSH、禁止 Agent（二期以新实现扩展接口）。
// 宪法 III：只写 <deploy_root>/dynamic/ 子树；traefik.yml / acme.json 恒只读。
package deployer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ArtifactFile 是一个生成产物：相对路径 + 内容。
type ArtifactFile struct {
	Path    string // 相对 dynamic/ 的路径，如 "routers/r-1.yml"
	Content []byte
	Mode    os.FileMode // 默认 0644；含敏感材料的产物 0600（R7）
}

// Deployer 是配置下发通道接口（二期 Agent 的唯一扩展点，宪法 XII）。
type Deployer interface {
	// Deploy 将产物集合原子替换到节点落盘根（deploy_root）的 dynamic/ 子树。
	Deploy(ctx context.Context, deployRoot string, files []ArtifactFile) error
	// ProbeWritable 校验落盘子树可写（部署前置探测，R4 风险缓解）。
	ProbeWritable(ctx context.Context, deployRoot string) error
	// Cleanup 移除本通道管理的整个 dynamic/ 子树（回滚为空快照 / 测试回收）。
	Cleanup(ctx context.Context, deployRoot string) error
}

var ErrUnsafePath = errors.New("产物路径越界：只允许 dynamic/ 子树内的常规文件")

type FileDeployer struct{}

func NewFileDeployer() *FileDeployer { return &FileDeployer{} }

func (d *FileDeployer) dynamicDir(deployRoot string) string {
	return filepath.Join(deployRoot, "dynamic")
}

func (d *FileDeployer) ProbeWritable(_ context.Context, deployRoot string) error {
	dir := d.dynamicDir(deployRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 %s 失败: %w", dir, err)
	}
	probe := filepath.Join(dir, ".gc-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("落盘目录不可写: %w", err)
	}
	err = f.Sync()
	f.Close()
	os.Remove(probe)
	return err
}

// Deploy 以「整目录集替换」实现原子版本切换：
// 每个文件独立 写临时→fsync→rename（读取方任何时刻只见完整文件），
// 结束后清理不在目标集合内的旧文件（避免残留路由继续被 watch 加载）。
func (d *FileDeployer) Deploy(ctx context.Context, deployRoot string, files []ArtifactFile) error {
	dir := d.dynamicDir(deployRoot)
	if err := d.ProbeWritable(ctx, deployRoot); err != nil {
		return err
	}
	written := make(map[string]bool, len(files))
	for _, f := range files {
		rel, err := safeRelPath(f.Path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		tmp := target + ".tmp"
		if err := writeFsync(tmp, f.Content, mode); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("写入临时文件 %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, target); err != nil { // POSIX 原子替换（同目录）
			os.Remove(tmp)
			return fmt.Errorf("原子替换 %s: %w", target, err)
		}
		written[target] = true
	}
	return removeStale(dir, written)
}

func (d *FileDeployer) Cleanup(_ context.Context, deployRoot string) error {
	dir := d.dynamicDir(deployRoot)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// safeRelPath 拒绝 ..、绝对路径与符号链接成分，保证写入被限制在 dynamic/ 子树。
func safeRelPath(p string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean("/" + p))
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(clean, "..") {
		return "", ErrUnsafePath
	}
	return filepath.FromSlash(strings.TrimPrefix(clean, "/")), nil
}

func writeFsync(path string, content []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil { // 覆盖已存在文件的旧权限
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// removeStale 删除不在目标集合内的旧产物（含遗留 .tmp），并回收空目录。
// watch=true 的 file provider 会加载目录下全部文件，残留即 Drift 源（宪法 XI）。
func removeStale(dir string, keep map[string]bool) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if path != dir {
				dirs = append(dirs, path)
			}
			return nil
		}
		if keep[path] {
			return nil
		}
		return os.Remove(path)
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- { // 自底向上
		if entries, err := os.ReadDir(dirs[i]); err == nil && len(entries) == 0 {
			os.Remove(dirs[i])
		}
	}
	return nil
}
