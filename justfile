build:
    go build -o git-backup .

install:
    go install .

check:
    go vet ./...
