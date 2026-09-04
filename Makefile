HOMEDIR := $(shell pwd)
BUILD_DIR  := $(HOMEDIR)/output

BUILD=$(shell git rev-parse --short HEAD)@$(shell date +%s)
CURRENT_OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
CURRENT_ARCH := $(shell uname -m | tr '[:upper:]' '[:lower:]')
LD_FLAGS=-ldflags "-X main.BuildVersion=$(BUILD)"
GO_BUILD=CGO_ENABLED=0 go build $(LD_FLAGS)

APPNAME = agent-runtime-manager

export GOENV = $(HOMEDIR)/go.env

prepare:
	git version
	go env
	go mod download || go mod download -x
    $(shell mkdir -p $(BUILD_DIR))


.PHONY: build
build: prepare
	GOOS=darwin GOARCH=arm64 $(GO_BUILD) -o $(BUILD_DIR)/bin/$(APPNAME)

.PHONY: buildLinuxX86
buildLinuxX86: prepare
	GOOS=linux GOARCH=amd64 $(GO_BUILD) -o $(BUILD_DIR)/bin/$(APPNAME)

package:
     $(shell cp -a conf $(BUILD_DIR)/conf)

clean:
	rm -rf $(BUILD_DIR)

.PHONY: lint
lint:
	golangci-lint run

.PHONY: format
format:
	golangci-lint fmt
	golangci-lint run --fix
	go fmt ./...
	go mod tidy


