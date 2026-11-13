default: build

build:
    go build ./...

install:
    go install ./cmd/gh-ff

test:
    go test ./...

# Release targets into dist/; tag HEAD first so the stamp is the tag.
dist:
    ./scripts/build.sh

# Uploads dist/ as a release: HEAD's tag, or next from the dev branch.
publish:
    ./scripts/publish.sh
