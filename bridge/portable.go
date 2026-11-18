package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func WebView2UserDataPath() string {
	return filepath.Join(Env.DataDir, ".cache", "webview2")
}

func initWindowsDataDir(configured string) error {
	previous, _ := defaultDataDir()
	dataDir, legacyDir, err := windowsDataPaths(Env.BasePath, configured, previous)
	if err != nil {
		return err
	}
	Env.DataDir = filepath.ToSlash(dataDir)
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("无法在程序旁创建数据目录 %q：%w", dataDir, err)
	}
	tempDir := filepath.Join(dataDir, ".cache", "tmp")
	webviewDir := WebView2UserDataPath()
	for _, dir := range []string{tempDir, webviewDir, filepath.Join(dataDir, "vpngate")} {
		actual, err := resolveCreationPath(dir)
		if err != nil || !strictlyInside(dataDir, actual) {
			return fmt.Errorf("本地依赖目录必须位于程序的数据目录内：%s", dir)
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("无法创建目录 %q：%w", dir, err)
		}
	}
	// WebView2 honors this environment variable ahead of the Wails option.
	// Keep the WebView2 profile and subprocess scratch data in this copy's directory.
	for name, value := range map[string]string{
		"WEBVIEW2_USER_DATA_FOLDER": webviewDir,
		"TEMP":                      tempDir,
		"TMP":                       tempDir,
		"TMPDIR":                    tempDir,
	} {
		if err = os.Setenv(name, value); err != nil {
			return fmt.Errorf("无法设置本地运行目录 %q：%w", value, err)
		}
	}
	if err = os.Unsetenv("WEBVIEW2_BROWSER_EXECUTABLE_FOLDER"); err != nil {
		return err
	}
	if err = migrateWindowsProfile(dataDir, legacyDir); err != nil {
		return fmt.Errorf("无法将旧配置复制到程序旁的目录：%w", err)
	}
	return nil
}

func windowsDataPaths(appDir, configured, previous string) (local, legacy string, err error) {
	root, err := filepath.EvalSymlinks(appDir)
	if err != nil {
		return "", "", err
	}
	if configured != "" {
		if !filepath.IsAbs(configured) {
			configured = filepath.Join(root, configured)
		}
		actual, err := resolveCreationPath(configured)
		if err != nil {
			return "", "", err
		}
		if strictlyInside(root, actual) {
			return actual, "", nil
		}
		previous = actual
	}
	local, err = resolveCreationPath(filepath.Join(root, "data"))
	if err != nil {
		return "", "", err
	}
	if !strictlyInside(root, local) {
		return "", "", fmt.Errorf("数据目录必须位于程序所在目录内：%s", local)
	}
	return local, previous, nil
}

// Resolve an existing parent first so a junction cannot move new data elsewhere.
func resolveCreationPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(path)
	if !errors.Is(err, os.ErrNotExist) || parent == path {
		return "", err
	}
	root, err := resolveCreationPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.Base(path)), nil
}

func strictlyInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && filepath.IsLocal(rel)
}

func readProfileState(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]json.RawMessage
	if err = json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func migrateWindowsProfile(local, old string) error {
	if old == "" || strings.EqualFold(filepath.Clean(local), filepath.Clean(old)) {
		return nil
	}
	target := filepath.Join(local, "vpngate", "state.json")
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source := filepath.Join(old, "vpngate", "state.json")
	state, err := readProfileState(source)
	if errors.Is(err, os.ErrNotExist) {
		source = filepath.Join(old, "vpngate-draft", "state.json")
		state, err = readProfileState(source)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// The upstream GUI can share the old Windows profile directory. Only a
	// profile that actually contains managed VPNGate settings is eligible.
	if len(state["settings"]) == 0 {
		return nil
	}
	for _, path := range []string{"user.yaml", "locales", "vpngate/cores", "vpngate/rules", "sing-box", "WebView2"} {
		if err = copyProfileAsset(filepath.Join(old, path), filepath.Join(local, path)); err != nil {
			return err
		}
	}
	// Publish the portable profile after its binaries are in place. Runtime and
	// WinINet snapshots describe the old directory's instance and stay there.
	return migrateDesktopState(target, []string{source})
}

func copyProfileAsset(source, target string) error {
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if targetInfo, err := os.Lstat(target); err == nil {
		if targetInfo.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !targetInfo.IsDir() {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".staging-") {
				continue
			}
			if err := copyProfileAsset(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	from, err := os.Open(source)
	if err != nil {
		return err
	}
	defer from.Close()
	dir := filepath.Dir(target)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".portable-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	_, copyErr := io.Copy(file, from)
	if err = errors.Join(copyErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(temp, target)
}
