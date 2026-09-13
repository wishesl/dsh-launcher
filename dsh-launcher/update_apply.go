package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// 跨平台落地（M4）：解包 + 替换
//
// 三个平台的产物形态不同（见 release.yml）：
//   - windows：裸 exe              → 直接重命名替换
//   - linux  ：tar.gz 里的 dsh-launcher 裸二进制
//   - darwin ：zip 里的 dsh-launcher.app 应用包（里面可能带符号链接）
//
// 替换策略三平台是同一条：**把正在运行的目标改名，再把新内容放到原路径**。
// Windows 不允许覆盖运行中的 exe；Linux 往里写会 ETXTBSY；macOS 的 .app 目录同理。
// 改名这条在三个平台都成立，而且运行中的老进程会继续跑完自己这一条命
// （本机 spike 验证过 Windows，Linux/macOS 的语义相同：inode 跟着进程走）。
// ---------------------------------------------------------------------------

// 解包上限：产物才十几 MB，给足余量但绝不无限解包（畸形/恶意压缩包的兜底）。
const maxExtractBytes = 512 << 20 // 512 MB

// updatePayloadKind 按资产名判断落地形态。
func updatePayloadKind(assetName string) string {
	n := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(n, ".zip"):
		return "zip"
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"):
		return "targz"
	default:
		return "raw" // .exe / 裸二进制（Windows）
	}
}

// stageUpdatePayload 把下载好的资产整理成"可以直接落地的那个东西"，返回其路径：
//   - raw   → 资产本身
//   - targz → 解出来的裸二进制
//   - zip   → 解出来的 .app 包目录（没有包时退化成单个可执行文件）
func stageUpdatePayload(assetPath, workDir, goos string) (string, error) {
	switch updatePayloadKind(assetPath) {
	case "targz":
		return extractTarGzBinary(assetPath, workDir, "dsh-launcher")
	case "zip":
		return extractZipPayload(assetPath, workDir)
	default:
		return assetPath, nil
	}
}

// applyUpdate 把 staged 落到 self 上（按平台分流）。
func applyUpdate(self, staged, goos string) error {
	if goos == "darwin" {
		if bundle := appBundleRoot(self); bundle != "" {
			return applyUpdateBundle(bundle, staged)
		}
		// 不是从 .app 里跑的（例如直接跑 Contents/MacOS 下的二进制）→ 按裸二进制处理。
	}
	return applyUpdateExecutable(self, staged)
}

// appBundleRoot 从可执行文件路径往上找到 .app 包根；不是包结构时返回 ""。
// 例：/Applications/DSH Launcher.app/Contents/MacOS/dsh-launcher → /Applications/DSH Launcher.app
func appBundleRoot(exe string) string {
	dir := filepath.Dir(exe)
	for i := 0; i < 4 && dir != "" && dir != string(os.PathSeparator) && dir != "."; i++ {
		if strings.HasSuffix(dir, ".app") {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// applyUpdateBundle 用 stagedApp 整体替换 bundle（macOS）。
//
// 老包改名成 <bundle>.old，新包搬到原路径 —— 名字沿用原来的（用户可能把它改过名），
// 所以"落地"的是新内容而不是新名字。失败一律回滚。
func applyUpdateBundle(bundle, stagedApp string) error {
	if _, err := os.Stat(stagedApp); err != nil {
		return fmt.Errorf("解包出来的应用包不可用：%w", err)
	}
	if err := ensureWritableDir(filepath.Dir(bundle)); err != nil {
		return fmt.Errorf("应用所在目录不可写（%s）—— 请用「打开 Release 页」手动替换", filepath.Dir(bundle))
	}
	old := bundle + ".old"
	_ = os.RemoveAll(old) // 上次的残留（正常情况下启动时已清掉）
	if err := os.Rename(bundle, old); err != nil {
		return fmt.Errorf("无法重命名正在运行的应用包（%w）—— 请用「打开 Release 页」手动替换", err)
	}
	if err := movePath(stagedApp, bundle); err != nil {
		if rbErr := os.Rename(old, bundle); rbErr != nil {
			return fmt.Errorf("写入新版本失败且回滚失败（%v / %v）—— 原程序在 %s，请手动改回", err, rbErr, old)
		}
		return fmt.Errorf("写入新版本失败（已回滚到原版本）：%w", err)
	}
	// 运行中的老进程还占着里面的可执行文件 → 删不掉是正常的，留给下次启动清理。
	_ = os.RemoveAll(old)
	return nil
}

// movePath 把 src 搬到 dst：同卷直接 rename，跨卷（比如 %APPDATA% 与 /Applications 不同盘）
// 退化成递归复制 + 删除源。
func movePath(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// copyTree 递归复制文件/目录/符号链接，保留权限位。
func copyTree(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		_ = os.Remove(dst)
		return os.Symlink(target, dst)
	case st.IsDir():
		if err := os.MkdirAll(dst, st.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(dst, st.Mode().Perm())
	case st.Mode().IsRegular():
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	default:
		return fmt.Errorf("不支持的文件类型: %s", src)
	}
}

// ---------------------------------------------------------------------------
// 解包
// ---------------------------------------------------------------------------

// safeArchivePath 判定压缩包内的相对路径是否安全：
// 必须是相对路径、不能有 ".."、不能是空/根。防的是 zip-slip / tar-slip
// （压缩包里的 ../../ 把文件写到目标目录之外）。
func safeArchivePath(p string) bool {
	if p == "" || p == "." {
		return false
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// withinDir 确认 target 落在 dir 之内（解包后二次校验，防御性）。
func withinDir(dir, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// extractTarGzBinary 从 tar.gz 里取出名为 want 的常规文件（其余一概不落地）。
//
// 两遍扫描：**先把整包的路径验完**再解包。只"边扫边取"的话，一旦目标文件排在前面就会
// 提前 return，后面那些带 ../ 的恶意条目根本没被检查到（测试抓过这个洞）。
func extractTarGzBinary(archivePath, destDir, want string) (string, error) {
	// 第一遍：路径安全（整包拒绝）+ 大小上限。
	var total int64
	if err := walkTarGz(archivePath, func(h *tar.Header, _ io.Reader) error {
		name := filepath.Clean(strings.TrimPrefix(h.Name, "./"))
		if !safeArchivePath(name) {
			return fmt.Errorf("压缩包里有非法路径：%s", h.Name)
		}
		total += h.Size
		if total > maxExtractBytes {
			return fmt.Errorf("压缩包内容过大（超过 %s）", humanSize(maxExtractBytes))
		}
		return nil
	}); err != nil {
		return "", err
	}

	// 第二遍：取目标文件。
	dest := filepath.Join(destDir, want)
	found := false
	if err := walkTarGz(archivePath, func(h *tar.Header, r io.Reader) error {
		name := filepath.Clean(strings.TrimPrefix(h.Name, "./"))
		if h.Typeflag != tar.TypeReg || filepath.Base(name) != want {
			return nil
		}
		if !withinDir(destDir, dest) {
			return fmt.Errorf("解包路径越界：%s", h.Name)
		}
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.CopyN(out, r, h.Size); err != nil {
			_ = out.Close()
			return fmt.Errorf("解包 %s 失败：%w", h.Name, err)
		}
		if err := out.Close(); err != nil {
			return err
		}
		found = true
		return nil
	}); err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("压缩包里没有 %s", want)
	}
	return dest, nil
}

// walkTarGz 顺序遍历 tar.gz 的每个条目（回调拿到 header 与可直接读的 reader）。
func walkTarGz(archivePath string, visit func(h *tar.Header, r io.Reader) error) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("不是有效的 tar.gz：%w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("解包失败：%w", err)
		}
		if err := visit(h, tr); err != nil {
			return err
		}
	}
}

// extractZipPayload 从 zip 里取出可落地的内容：
// 优先整个 .app 包（macOS，含目录/符号链接/权限位）；没有包时退化成单个可执行文件。
func extractZipPayload(archivePath, destDir string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("不是有效的 zip：%w", err)
	}
	defer zr.Close()

	// 第一遍：**先整体验路径安全**，任何一个条目带 ../ 或绝对路径就整包拒绝。
	// （不能只在真正解包时逐个跳过：静默跳过恶意条目更隐蔽，直接拒绝更好排查。）
	for _, f := range zr.File {
		if !safeArchivePath(filepath.Clean(f.Name)) {
			return "", fmt.Errorf("压缩包里有非法路径：%s", f.Name)
		}
	}

	// 顶层目录：第一个以 .app 结尾的一级路径。
	root := ""
	for _, f := range zr.File {
		name := filepath.Clean(f.Name)
		first := strings.Split(filepath.ToSlash(name), "/")[0]
		if strings.HasSuffix(first, ".app") {
			root = first
			break
		}
	}

	var total int64
	if root == "" {
		// 退化路径：单个常规文件（例如有人把裸二进制打成 zip）。
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			dest := filepath.Join(destDir, filepath.Base(filepath.Clean(f.Name)))
			if !withinDir(destDir, dest) {
				return "", fmt.Errorf("解包路径越界：%s", f.Name)
			}
			if err := extractZipEntry(f, dest); err != nil {
				return "", err
			}
			return dest, nil
		}
		return "", fmt.Errorf("压缩包里没有可落地的文件")
	}

	appDir := filepath.Join(destDir, root)
	for _, f := range zr.File {
		name := filepath.Clean(f.Name)
		if name != root && !strings.HasPrefix(filepath.ToSlash(name), root+"/") {
			continue
		}
		dest := filepath.Join(destDir, name)
		if !withinDir(destDir, dest) {
			return "", fmt.Errorf("解包路径越界：%s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, f.Mode().Perm()|0o700); err != nil {
				return "", err
			}
			continue
		}
		total += int64(f.UncompressedSize64)
		if total > maxExtractBytes {
			return "", fmt.Errorf("压缩包内容过大（超过 %s）", humanSize(maxExtractBytes))
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if err := extractZipEntry(f, dest); err != nil {
			return "", err
		}
	}
	// 万一包里的可执行位没带过来（老式 zip 打包），这里补一下 —— 少了它新版本根本起不来。
	_ = chmodExecutables(filepath.Join(appDir, "Contents", "MacOS"))
	return appDir, nil
}

// extractZipEntry 落地一个 zip 条目：符号链接按链接建，常规文件写内容并保留权限位。
func extractZipEntry(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	if f.Mode()&os.ModeSymlink != 0 {
		target, err := io.ReadAll(io.LimitReader(rc, 4096))
		if err != nil {
			return err
		}
		_ = os.Remove(dest)
		return os.Symlink(strings.TrimSpace(string(target)), dest)
	}

	perm := f.Mode().Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// chmodExecutables 给目录下的常规文件补上可执行位（已有则不动）。
func chmodExecutables(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 == 0 {
			_ = os.Chmod(p, info.Mode().Perm()|0o755)
		}
	}
	return nil
}
