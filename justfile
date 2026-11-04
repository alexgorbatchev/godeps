# godeps justfile

# Default recipe: list available recipes
default:
    @just --list

# Run all unit tests and require 90% statement coverage
test:
    go test -v -race -coverprofile=coverage.out ./...
    awk 'NR > 1 { stmts[$1] = $2; if ($3 > 0) hit[$1] = 1 } END { for (b in stmts) { total += stmts[b]; if (b in hit) covered += stmts[b] } if (total == 0) { print "coverage.out has no statements"; exit 1 } printf "total coverage: %.2f%% (%d of %d statements)\n", 100 * covered / total, covered, total; if (covered * 100 < total * 90) { print "coverage is below the required 90%"; exit 1 } }' coverage.out

# Build for the host and for Windows
build:
    go build ./...
    GOOS=windows go build ./...

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
