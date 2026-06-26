SHELL := /bin/bash

.PHONY: fmt clean vet test build lint check

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

build:
	go build ./...

lint:
	golangci-lint run

check: fmt vet lint test
