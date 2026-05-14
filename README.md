# git-backup

A script for cloning GitHub repositories as bare mirrors, so you have a local backup in case GitHub goes down.

## What it does

Clones repositories in bare format (`--mirror`), which captures all refs (branches, tags, etc.) and keeps them in sync on subsequent runs. Useful as an offline fallback when remote hosting is unavailable.

## Usage

```sh
./backup.sh <github-user-or-org>
```

This clones (or updates) all public repositories for the given user or organization into the current directory as bare repos.

## License

MIT
