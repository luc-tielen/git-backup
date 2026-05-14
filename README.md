# git-backup

A script for cloning GitHub repositories as bare mirrors, so you have a local backup in case GitHub goes down.

## What it does

Reads a YAML config describing your repositories, then for each one:
- **First run**: clones it as a bare mirror (`git clone --mirror`), capturing all refs (branches, tags, etc.)
- **Subsequent runs**: fetches the latest changes (`git remote update`)

## Installation

```sh
git clone git@github.com:luc-tielen/git-backup.git
cd git-backup
go install .
```

The `git-backup` binary is placed in `~/go/bin/`.

## Configuration

Config file lives at `~/.config/git-backup/config.yaml`. Generate a placeholder:

```sh
git-backup --create-config
```

Shape:

```yaml
backup-dir: ~/backups/git
repositories:
  personal:
    my-repo: git@github.com:username/my-repo.git
  work:
    work-repo: git@github.com:org/work-repo.git
```

- `backup-dir`: where bare repos are stored (tilde-expanded, created if missing)
- `repositories`: nested map of `directory → project → remote-url`; repos land at `<backup-dir>/<directory>/<project>.git`

## Usage

```sh
git-backup
```

Run periodically (e.g. via cron) to keep mirrors up to date. Per-repo errors are non-fatal — a failure on one repo is logged and the rest continue.

## License

MIT
