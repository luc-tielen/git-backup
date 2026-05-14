build:
    go build -o git-backup .

install:
    go install .

test:
    go test ./...

lint:
    golangci-lint run

format:
    gofmt -w .

check: format lint test
