.PHONY: build proper help docs test
help:
	@echo valid targets: build proper clean test
build: proper build_gen 
proper:
	find . -name '*.go' | sort | xargs wc -l
	gofmt -s -w $(shell go list -f '{{.Dir}}' ./...)
	@if command -v goimports >/dev/null 2>&1; then \
		echo goimports -w $(shell go list -f '{{.Dir}}' ./...); \
		goimports -w $(shell go list -f '{{.Dir}}' ./...); \
	fi
	golangci-lint run --tests=false
test:
	go test ./...
build_gen: 
	go build -o retina-generator .
clean:
	rm -f retina-generator
