package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// ```plugins.yml
// start:
//   - repo: username/repo1
//     version: v1.0.0
//     url: https://github.com/username/repo1/archive/refs/tags/v1.0.0.zip
//   - repo: username/repo2
//     version: main
//     url: https://github.com/username/repo2/archive/refs/heads/main.zip
//
// opt:
//   - repo: username/repo3
//     version: main
//     url: https://github.com/username/repo3/archive/refs/heads/main.zip
// ```

type Plugin struct {
	Repo    string `yaml:"repo"`
	Version string `yaml:"version"`
	Url     string `yaml:"url"`
}

type Plugins struct {
	Start []Plugin `yaml:"start"`
	Opt   []Plugin `yaml:"opt"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %v\n", err)
		os.Exit(1)
	}
}

func run() error {

	// plugins.ymlの取得
	pluginsFilePath := getPluginsFilePath()

	// packフォルダパスの取得
	err, packPath := getPackDir()
	if err != nil {
		return err
	}

	if len(os.Args) < 2 {
		return errors.New("コマンドを指定してください。")
	}
	cmd := os.Args[1]
	switch cmd {
	case "add":
		add(pluginsFilePath)
	case "rm":
		remove()
	case "sync":
		return sync(pluginsFilePath, packPath)
	default:
		return errors.New("存在しないコマンドです。")
	}

	return nil
}

func add(pluginsFilePath string) error {

	if len(os.Args) < 4 {
		return errors.New("引数が不足しています。")
	}

	plugins, err := readPlugins(pluginsFilePath)

	startOrOpt := os.Args[2]
	zipUrl := os.Args[3]
	repo, version, err := extractRepoAndVersionPath(zipUrl)
	if err != nil {
		return err
	}

	plugin := Plugin{repo, version, zipUrl}

	if startOrOpt == "start" {
		isContain := false
		for _, p := range plugins.Start {
			if p.Repo == plugin.Repo {
				isContain = true
			}
		}
		if !isContain {
			plugins.Start = append(plugins.Start, plugin)
		} else {
			fmt.Printf("%s already exists", plugin.Repo)
			return nil
		}

	} else if startOrOpt == "opt" {
		isContain := false
		for _, p := range plugins.Opt {
			if p.Repo == plugin.Repo {
				isContain = true
			}
		}
		if !isContain {
			plugins.Opt = append(plugins.Opt, plugin)
		}
	}

	// write
	data, err := yaml.Marshal(plugins)
	if err != nil {
		return err
	}

	err = os.WriteFile(pluginsFilePath, data, 0644)
	if err != nil {
		return err
	}

	fmt.Printf("added : %v\n", plugin)

	return nil
}

func extractRepoAndVersionPath(rawURL string) (string, string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("URLの解析に失敗しました: %w", err)
	}

	// パスを分割して "owner/repo" を取得
	segments := strings.Split(parsedURL.Path, "/")
	if len(segments) < 3 {
		return "", "", fmt.Errorf("URLの形式が正しくありません: %s", rawURL)
	}

	owner := segments[1]
	repo := segments[2]

	p := parsedURL.Path
	b := path.Base(p)
	v := strings.TrimRight(b, ".zip")

	return path.Join(owner, repo), v, nil
}

func remove() error {
	fmt.Println("remove")
	return nil
}

func sync(pluginsFilePath, packPath string) error {

	packPaths := [2]string{filepath.Join(packPath, "start"), filepath.Join(packPath, "opt")}

	plugins, err := readPlugins(pluginsFilePath)
	if err != nil {
		return err
	}
	pluginsMaps := [2]map[string]string{makePluginsMap(plugins.Start), makePluginsMap(plugins.Opt)}

	// 前処理 -------------------------------------
	for _, p := range packPaths {
		os.MkdirAll(p, 0755)
	}

	// ゴミ掃除 -----------------------------------
	fmt.Printf("[%-5s] gc\n", "start")

	for i, pth := range packPaths {
		existedPlugins, err := listDirEntries(pth)
		if err != nil {
			return err
		}

		for _, entry := range existedPlugins {
			if _, ok := pluginsMaps[i][filepath.Base(entry)]; ok {
				// exist
			} else {
				// not exist
				if err := os.RemoveAll(entry); err != nil {
					return err
				}
				fmt.Printf("%-7s removed: %s\n", " ", filepath.Base(entry))
			}
		}

	}
	fmt.Printf("[%-5s] gc\n", "end")

	// インストール --------------------------------
	fmt.Printf("[%-5s] install\n", "start")
	ps1, err := listDirEntries(packPaths[0])
	if err != nil {
		return err
	}
	ps2, err := listDirEntries(packPaths[1])
	if err != nil {
		return err
	}
	existedPluginLists := [2][]string{ps1, ps2}
	startOrOpt := make([]string, 2, 2)
	startOrOpt[0] = "start"
	startOrOpt[1] = "opt"

	for i, startOptPlugins := range [2][]Plugin{plugins.Start, plugins.Opt} {
		existedPlugins := existedPluginLists[i]

		for _, p := range startOptPlugins {
			dirName := makeDirName(p)
			if slices.Contains(existedPlugins, filepath.Join(packPaths[i], dirName)) {
				continue
			}

			zipPath := filepath.Join(packPaths[i], dirName+".zip")
			if err := downloadZip(p.Url, zipPath); err != nil {
				return err
			}

			expandedPath := filepath.Join(packPaths[i], dirName)
			if err := unzipWithoutTopLevel(zipPath, expandedPath); err != nil {
				return err
			}

			if err := os.Remove(zipPath); err != nil {
				return err
			}
			fmt.Printf("%-7s installed to %-5s: %s\n", " ", startOrOpt[i], dirName)
		}

	}

	fmt.Printf("[%-5s] install\n", "end")
	return nil
}

func listDirEntries(dirPath string) ([]string, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, entry := range entries {
		fullPath := filepath.Join(dirPath, entry.Name())
		paths = append(paths, fullPath)
	}
	return paths, nil
}

func readPlugins(path string) (*Plugins, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var plugins Plugins
	if err := yaml.Unmarshal(data, &plugins); err != nil {
		return nil, err
	}

	return &plugins, nil
}

func makePluginsMap(plugins []Plugin) map[string]string {
	pluginsMap := make(map[string]string)
	for _, p := range plugins {
		key := makeDirName(p)
		pluginsMap[key] = p.Version
	}
	return pluginsMap
}

func getPackDir() (error, string) {
	cmd := exec.Command("nvim", "--headless", "-c", "lua io.stdout:write(vim.o.packpath)", "-c", "qa")
	output, err := cmd.Output()
	if err != nil {
		return err, ""
	}
	dir := filepath.Join(string(output), "pack", "noclonpack")
	return nil, dir
}

func getPluginsFilePath() string {

	fileName := "plugins.yml"

	// XDG_CONFIG_HOMEの取得
	xdgConfigHome := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "nvim", fileName)
	}

	switch runtime.GOOS {
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			return filepath.Join(localAppData, "nvim", fileName)
		} else {
			return fileName
		}
	default:
		return filepath.Join("~", ".config", "nvim", fileName)
	}

}

func makeDirName(plugin Plugin) string {
	return path.Base(plugin.Repo)
}

func downloadZip(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func unzipWithoutTopLevel(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	// トップレベルディレクトリ名を特定
	topLevelDir := ""
	for _, f := range r.File {
		parts := strings.Split(f.Name, "/")
		if len(parts) > 1 {
			if topLevelDir == "" {
				topLevelDir = parts[0]
			} else if topLevelDir != parts[0] {
				topLevelDir = ""
				break
			}
		} else {
			topLevelDir = ""
			break
		}
	}

	for _, f := range r.File {
		// トップレベルディレクトリを除外
		relPath := f.Name
		if topLevelDir != "" {
			if strings.HasPrefix(f.Name, topLevelDir+"/") {
				relPath = strings.TrimPrefix(f.Name, topLevelDir+"/")
			} else {
				// 一致しない場合はそのまま
				relPath = f.Name
			}
		}

		fpath := filepath.Join(dest, relPath)

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)

		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}
