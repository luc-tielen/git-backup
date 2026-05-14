package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
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
`

type Config struct {
	BackupDir    string                       `yaml:"backup-dir"`
	Repositories map[string]map[string]string `yaml:"repositories"`
}

// gitExec is the function used to run git commands; replaced in tests.
var gitExec = defaultGitExec

func defaultGitExec(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	lg := log.New(os.Stderr, "git-backup: ", 0)
	os.Exit(run(os.Args[1:], lg))
}

func run(args []string, lg *log.Logger) int {
	flags := flag.NewFlagSet("git-backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	createConfig := flags.Bool("create-config", false, "write a placeholder config file and exit")
	if err := flags.Parse(args); err != nil {
		lg.Printf("usage: git-backup [--create-config]")
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

	if err := validateConfig(cfg); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

func validateConfig(cfg Config) error {
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
