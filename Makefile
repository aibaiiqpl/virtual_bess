# Usage:
#   make              - build for current platform
#   make all          - build for all target platforms
#   make linux-arm64  - build for Linux ARM64
#   make linux-arm64-iec61850 - build for Linux ARM64 with IEC 61850
#   make linux-armv7  - build for Linux ARMv7
#   make clean        - remove build artifacts
#
# 版本信息通过 -ldflags 注入 internal/buildinfo，运行 `virtual_bess details` 可查看，
# 用于确认现场部署的到底是哪一次构建。

BINARY := virtual_bess
BUILD_DIR := build
PKG := virtual_bess/internal/buildinfo

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildTime=$(BUILD_TIME)

# $(BINARY) 也声明为 phony：目标文件存在时 make 会误判为最新，导致改了代码却没重新构建。
.PHONY: all clean $(BINARY) linux-amd64 linux-arm64 linux-arm64-iec61850 linux-armv7

all: linux-amd64 linux-arm64 linux-armv7

$(BINARY):
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/virtual_bess

linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/virtual_bess

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/virtual_bess

linux-arm64-iec61850:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc \
		go build -tags iec61850 -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64-iec61850 ./cmd/virtual_bess

linux-armv7:
	GOOS=linux GOARCH=arm GOARM=7 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-armv7 ./cmd/virtual_bess

clean:
	rm -rf $(BUILD_DIR) $(BINARY)
