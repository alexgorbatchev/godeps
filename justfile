# godeps justfile

# Default recipe: list available recipes
default:
    @just --list

# Run all unit tests and require 90% statement coverage
test:
    go test -v -race -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | awk '/^total:/ { sub("%", "", $3); printf "total coverage: %s%%\n", $3; if ($3 + 0 < 90) { print "coverage is below the required 90%"; exit 1 } }'

# Run static code analysis for the host and for Windows
vet:
    go vet ./...
    GOOS=windows go vet ./...

# Format Go code
fmt:
    go fmt ./...

# Run module hygiene check
tidy:
    go mod tidy -diff

# Clean temporary and test artifacts
clean:
    rm -rf bin dist coverage.out .tmp

# Download project dependencies for local development
setup:
    go mod download
