# godeps justfile

# Default recipe: list available recipes
default:
    @just --list

# Run all unit tests
test:
	go test -v -race -coverprofile=coverage.out ./...

# Run static code analysis
vet:
	go vet ./...

# Format Go code
fmt:
	go fmt ./...

# Run module hygiene check
tidy:
	go mod tidy -diff

# Clean temporary and test artifacts
clean:
	rm -rf bin dist coverage.out .tmp
