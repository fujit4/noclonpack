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

// ```noclonpack_plugins.yml
// start:
//   - repo: username/repo1
//     url: https://github.com/<username>/<repo1>/archive/refs/tags/v1.0.0.zip
//   - repo: username/repo2
//     url: https://github.com/<username>/<repo2>/archive/refs/heads/main.zip
//
// opt:
//   - repo: username/repo3
//     url: https://github.com/<username>/<repo3>/archive/refs/heads/main.zip
// ```

var (
	Version = "v.X.X.X"
)

type Plugin struct {
	Repo    string `yaml:"repo"`
	Url     string `yaml:"url"`
}

type Plugins struct {
	Start []Plugin `yaml:"start"`
	Opt   []Plugin `yaml:"opt"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {

	// noclonpack_plugins.ymlの取得
	pluginsFilePath := getPluginsFilePath()

	// packフォルダパスの取得
	err, packPath := getPackDir()
	if err != nil {
		return err
	}

	if len(os.Args) < 2 {
		return errors.New("Please specify a command")
	}
	cmd := os.Args[1]
	switch cmd {
	case "add":
		return add(pluginsFilePath)
	case "list":
		return list(pluginsFilePath)
	case "rm":
		return remove(pluginsFilePath)
	case "sync":
		return sync(pluginsFilePath, packPath)
	case "version":
		fmt.Println(Version)
		return nil
	case "help":
		return help()
	default:
		return errors.New("Invalid command")
	}
}

func help() error {
	fmt.Println(`Usage: noclonpack <command> [arguments]

Commands:
  sync                 Sync plugins based on noclonpack_plugins.yml
  add <dir> <url>      Add a plugin to 'start' or 'opt' from a zip URL
  rm <repo>            Remove a plugin by repository name
  list                 List plugins from noclonpack_plugins.yml
  help                 Display usage
  version              Display the version of noclonpack

Examples:
  noclonpack sync
  noclonpack add start https://github.com/<user>/<repo>/archive/refs/heads/main.zip
  noclonpack rm user/repo
  noclonpack list
  noclonpack help
  noclonpack version
`)
	return nil
}

func add(pluginsFilePath string) error {

	if len(os.Args) < 4 {
		return errors.New("Insufficient arguments")
	}

	plugins, err := readPlugins(pluginsFilePath)
	if err != nil {
		return err
	}
	

	startOrOpt := os.Args[2]
	zipUrl := os.Args[3]
	repo,  err := extractRepoPath(zipUrl)
	if err != nil {
		return err
	}

	plugin := Plugin{repo, zipUrl}

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
	} else {
		return errors.New("Please specify 'start' or 'opt' as the second argument")
	}

	// write
	if err := writePlugins(plugins, pluginsFilePath); err != nil {
		return err
	}

	fmt.Printf("added : %v\n", plugin)

	return nil
}

func extractRepoPath(rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("Failed to parse URL: %w", err)
	}

	// パスを分割して "owner/repo" を取得
	segments := strings.Split(parsedURL.Path, "/")
	if len(segments) < 3 {
		return "", fmt.Errorf("Invalid URL format: %s", rawURL)
	}

	owner := segments[1]
	repo := segments[2]

	return path.Join(owner, repo), nil
}

func list(pluginsFilePath string) error {
	data, err := os.ReadFile(pluginsFilePath)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func remove(pluginsFilePath string) error {
	if len(os.Args) < 4 {
		return errors.New("Insufficient arguments")
	}
	startOrOpt := os.Args[2]
	repo := os.Args[3]

	plugins, err := readPlugins(pluginsFilePath)
	if err != nil {
		return err
	}

	var target Plugin
	deleted := false
	if startOrOpt == "start" {
		for i, p := range plugins.Start {
			if p.Repo == repo {
				target = plugins.Start[i]
				deleted = true
				plugins.Start = append(plugins.Start[:i], plugins.Start[i+1:]...)
				slices.Delete(plugins.Start, i, i+1)
				break
			}
		}
	} else if startOrOpt == "opt" {
		for i, p := range plugins.Opt {
			if p.Repo == repo {
				target = plugins.Opt[i]
				deleted = true
				plugins.Opt = append(plugins.Opt[:i], plugins.Opt[i+1:]...)
				break
			}
		}
	} else {
		return errors.New("Please specify 'start' or 'opt' as the second argument")
	}

	if deleted {
		if err := writePlugins(plugins, pluginsFilePath); err != nil {
			return err
		}
		fmt.Printf("removed : %v\n", target)
	} else {
		fmt.Printf("removed : ", "nothing")
	}


	return nil
}

func sync(pluginsFilePath, packPath string) error {

	packPaths := [2]string{filepath.Join(packPath, "start"), filepath.Join(packPath, "opt")}

	plugins, err := readPlugins(pluginsFilePath)
	if err != nil {
		return err
	}
	pluginsMaps := [2]map[string]bool{makePluginsMap(plugins.Start), makePluginsMap(plugins.Opt)}

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
	var plugins Plugins
	data, err := os.ReadFile(path)
	if err != nil {
		return &plugins, nil
	}

	if err := yaml.Unmarshal(data, &plugins); err != nil {
		return nil, err
	}

	return &plugins, nil
}

func makePluginsMap(plugins []Plugin) map[string]bool {
	pluginsMap := make(map[string]bool)
	for _, p := range plugins {
		key := makeDirName(p)
		pluginsMap[key] = true
	}
	return pluginsMap
}

func getPackDir() (error, string) {
	cmd := exec.Command("nvim", "--headless", "-c", "lua io.stdout:write(vim.o.packpath)", "-c", "qa")
	output, err := cmd.Output()
	if err != nil {
		return err, ""
	}
	rowdir := strings.Split(string(output), ",")[0]
	dir := filepath.Join(rowdir, "pack", "noclonpack")
	return nil, dir
}

func getPluginsFilePath() string {

	fileName := "noclonpack_plugins.yml"

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



func writePlugins(plugins *Plugins, pluginsFilePath string) error {
	p1 := plugins.Start
	slices.SortStableFunc(p1, func(a,b Plugin) int {
		if a.Repo < b.Repo {
			return -1
		} else {
			return 1
		}
	})
	plugins.Start = p1

	p2 := plugins.Opt
	slices.SortStableFunc(p2, func(a,b Plugin) int {
		if a.Repo < b.Repo {
			return -1
		} else {
			return 1
		}
	})
	plugins.Opt = p2

	data, err := yaml.Marshal(plugins)
	if err != nil {
		return err
	}

	err = os.WriteFile(pluginsFilePath, data, 0644)
	if err != nil {
		return err
	}

	return nil
}

