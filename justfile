default: build

build:
    go build ./...

install:
    go install ./cmd/gh-ff

test:
    go test ./...
