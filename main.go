package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const configPath = "~/.config/git-backup/config.yaml"

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

func main() {
	log.SetFlags(0)
	log.SetPrefix("git-backup: ")

	createConfig := flag.Bool("create-config", false, "write a placeholder config file and exit")
	flag.Parse()

	cfgPath := expandTilde(configPath)

	if *createConfig {
		if _, err := os.Stat(cfgPath); err == nil {
			log.Printf("config already exists at %s", cfgPath)
			os.Exit(0)
		}
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			log.Fatalf("creating config directory: %v", err)
		}
		if err := os.WriteFile(cfgPath, []byte(placeholderConfig), 0o644); err != nil {
			log.Fatalf("writing config: %v", err)
		}
		log.Printf("placeholder config written to %s", cfgPath)
		os.Exit(0)
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Fatal(err)
	}

	backupDir := expandTilde(cfg.BackupDir)
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		log.Printf("creating backup directory %s", backupDir)
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			log.Fatalf("creating backup directory: %v", err)
		}
	}

	hadError := false
	dirs := sortedKeys(cfg.Repositories)
	for _, dir := range dirs {
		projects := cfg.Repositories[dir]
		dirPath := filepath.Join(backupDir, dir)
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			log.Printf("creating directory %s: %v", dirPath, err)
			hadError = true
			continue
		}

		for _, project := range sortedKeys(projects) {
			url := projects[project]
			destPath := filepath.Join(dirPath, project+".git")

			if _, err := os.Stat(destPath); err == nil {
				log.Printf("updating %s/%s", dir, project)
				if err := runGit("-C", destPath, "remote", "update"); err != nil {
					log.Printf("error updating %s/%s: %v", dir, project, err)
					hadError = true
				}
			} else {
				log.Printf("cloning %s/%s from %s", dir, project, url)
				if err := runGit("clone", "--mirror", url, destPath); err != nil {
					log.Printf("error cloning %s/%s: %v", dir, project, err)
					hadError = true
				}
			}
		}
	}

	if hadError {
		os.Exit(1)
	}
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

func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
