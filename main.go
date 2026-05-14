package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "~/.config/git-backup/config.yaml"

const placeholderConfig = `backup-dir: ~/backups/git

repositories:
  personal:
    my-repo: git@github.com:username/my-repo.git
  work:
    work-repo: git@github.com:org/work-repo.git

scan:
  users:
    - username
  orgs:
    - my-org
`

type ScanConfig struct {
	Users []string `yaml:"users"`
	Orgs  []string `yaml:"orgs"`
}

type Config struct {
	BackupDir    string                       `yaml:"backup-dir"`
	Repositories map[string]map[string]string `yaml:"repositories"`
	Scan         *ScanConfig                  `yaml:"scan,omitempty"`
}

type repoInfo struct {
	Name   string
	SSHURL string
}

// gitExec is the function used to run git commands; replaced in tests.
var gitExec = defaultGitExec

func defaultGitExec(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// fetchRepos fetches all repos for a GitHub user or org; replaced in tests.
var fetchRepos = defaultFetchRepos

func defaultFetchRepos(owner string, isOrg bool, token string) ([]repoInfo, error) {
	var all []repoInfo
	for page := 1; ; page++ {
		var apiPath string
		if isOrg {
			apiPath = fmt.Sprintf("/orgs/%s/repos", owner)
		} else {
			apiPath = fmt.Sprintf("/users/%s/repos", owner)
		}
		url := fmt.Sprintf("https://api.github.com%s?type=all&per_page=100&page=%d", apiPath, page)

		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GitHub API request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub API returned %d for %s", resp.StatusCode, url)
		}

		var pageRepos []struct {
			Name   string `json:"name"`
			SSHURL string `json:"ssh_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&pageRepos); err != nil {
			return nil, fmt.Errorf("decoding GitHub API response: %w", err)
		}
		for _, r := range pageRepos {
			all = append(all, repoInfo{Name: r.Name, SSHURL: r.SSHURL})
		}
		if len(pageRepos) < 100 {
			break
		}
	}
	return all, nil
}

// getGitHubToken returns a GitHub token from GITHUB_TOKEN or `gh auth token`.
func getGitHubToken() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func main() {
	lg := log.New(os.Stderr, "git-backup: ", 0)
	os.Exit(run(os.Args[1:], lg))
}

func run(args []string, lg *log.Logger) int {
	if len(args) > 0 && args[0] == "scan" {
		return runScan(expandTilde(defaultConfigPath), lg, os.Stdout)
	}

	flags := flag.NewFlagSet("git-backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	createConfig := flags.Bool("create-config", false, "write a placeholder config file and exit")
	if err := flags.Parse(args); err != nil {
		lg.Print("usage: git-backup [--create-config] [scan]")
		return 1
	}

	cfgPath := expandTilde(defaultConfigPath)
	if *createConfig {
		return runCreateConfig(cfgPath, lg)
	}
	return runBackup(cfgPath, lg)
}

func runCreateConfig(cfgPath string, lg *log.Logger) int {
	if _, err := os.Stat(cfgPath); err == nil {
		lg.Printf("config already exists at %s", cfgPath)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		lg.Printf("creating config directory: %v", err)
		return 1
	}
	if err := os.WriteFile(cfgPath, []byte(placeholderConfig), 0o644); err != nil {
		lg.Printf("writing config: %v", err)
		return 1
	}
	lg.Printf("placeholder config written to %s", cfgPath)
	return 0
}

func runBackup(cfgPath string, lg *log.Logger) int {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		lg.Print(err)
		return 1
	}
	if err := validateBackupConfig(cfg); err != nil {
		lg.Printf("invalid config: %v", err)
		return 1
	}

	backupDir := expandTilde(cfg.BackupDir)
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		lg.Printf("creating backup directory %s", backupDir)
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			lg.Printf("creating backup directory: %v", err)
			return 1
		}
	}

	hadError := false
	for _, dir := range sortedKeys(cfg.Repositories) {
		projects := cfg.Repositories[dir]
		dirPath := filepath.Join(backupDir, dir)
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			lg.Printf("creating directory %s: %v", dirPath, err)
			hadError = true
			continue
		}

		for _, project := range sortedKeys(projects) {
			url := projects[project]
			destPath := filepath.Join(dirPath, project+".git")

			if _, err := os.Stat(destPath); err == nil {
				lg.Printf("updating %s/%s", dir, project)
				if err := gitExec("-C", destPath, "remote", "update"); err != nil {
					lg.Printf("error updating %s/%s: %v", dir, project, err)
					hadError = true
				}
			} else {
				lg.Printf("cloning %s/%s from %s", dir, project, url)
				if err := gitExec("clone", "--mirror", url, destPath); err != nil {
					lg.Printf("error cloning %s/%s: %v", dir, project, err)
					hadError = true
				}
			}
		}
	}

	if hadError {
		return 1
	}
	return 0
}

func runScan(cfgPath string, lg *log.Logger, out io.Writer) int {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		lg.Print(err)
		return 1
	}
	if cfg.Scan == nil || (len(cfg.Scan.Users) == 0 && len(cfg.Scan.Orgs) == 0) {
		lg.Print("no scan targets configured — add 'scan.users' or 'scan.orgs' to config")
		return 1
	}

	managed := map[string]bool{}
	for _, projects := range cfg.Repositories {
		for _, url := range projects {
			managed[url] = true
		}
	}

	token := getGitHubToken()
	hadUnmanaged := false
	fetchOK := true

	check := func(owner string, isOrg bool) {
		repos, err := fetchRepos(owner, isOrg, token)
		if err != nil {
			lg.Printf("fetching repos for %s: %v", owner, err)
			fetchOK = false
			return
		}
		for _, r := range repos {
			if !managed[r.SSHURL] {
				fmt.Fprintf(out, "%s/%s: %s\n", owner, r.Name, r.SSHURL)
				hadUnmanaged = true
			}
		}
	}

	for _, user := range cfg.Scan.Users {
		lg.Printf("scanning user %s", user)
		check(user, false)
	}
	for _, org := range cfg.Scan.Orgs {
		lg.Printf("scanning org %s", org)
		check(org, true)
	}

	if !fetchOK {
		return 1
	}
	if hadUnmanaged {
		return 1
	}
	lg.Print("all repos are managed")
	return 0
}

func loadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("config file not found at %s — run with --create-config to create one", path)
	}
	if err != nil {
		return Config{}, fmt.Errorf("opening config: %w", err)
	}
	defer f.Close()

	var cfg Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func validateBackupConfig(cfg Config) error {
	if cfg.BackupDir == "" {
		return errors.New("backup-dir must not be empty")
	}
	if len(cfg.Repositories) == 0 {
		return errors.New("repositories must not be empty")
	}
	for dir, projects := range cfg.Repositories {
		for project, url := range projects {
			if url == "" {
				return fmt.Errorf("repositories.%s.%s: remote URL must not be empty", dir, project)
			}
		}
	}
	return nil
}

func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
