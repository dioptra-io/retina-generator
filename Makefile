.PHONY: build lint fmt tidy test clean help

help:
	@echo "Valid targets:"
	@echo "  build  - Format, lint, and build retina-generator binary"
	@echo "  lint   - Format code and run linters"
	@echo "  fmt    - Format code"
	@echo "  tidy   - Tidy go modules"
	@echo "  test   - Run tests with race detection"
	@echo "  clean  - Remove built binaries"

build: lint
	go build -o retina-generator .

lint: fmt
	golangci-lint run

fmt:
	go fmt ./...

tidy:
	go mod tidy

test:
	go test -v -race -cover ./...

clean:
	rm -f retina-generator