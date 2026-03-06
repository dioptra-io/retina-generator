.PHONY: build proper help docs test clean

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
	golangci-lint run

test:
	go test -v -race -cover ./...

build_gen:
	go build -o retina-generator .

clean:
	rm -f retina-generator