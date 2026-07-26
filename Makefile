# Usage:
#   make              - build for current platform
#   make all          - build for all target platforms
#   make linux-arm64  - build for Linux ARM64
#   make linux-arm64-iec61850 - build for Linux ARM64 with IEC 61850
#   make linux-armv7  - build for Linux ARMv7
#   make clean        - remove build artifacts

BINARY := virtual_bess
BUILD_DIR := build

.PHONY: all clean linux-amd64 linux-arm64 linux-arm64-iec61850 linux-armv7

all: linux-amd64 linux-arm64 linux-armv7

$(BINARY):
	go build -o $(BINARY) ./cmd/virtual_bess

linux-amd64:
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/virtual_bess

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/virtual_bess

linux-arm64-iec61850:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc \
		go build -tags iec61850 -o $(BUILD_DIR)/$(BINARY)-linux-arm64-iec61850 ./cmd/virtual_bess

linux-armv7:
	GOOS=linux GOARCH=arm GOARM=7 go build -o $(BUILD_DIR)/$(BINARY)-linux-armv7 ./cmd/virtual_bess

clean:
	rm -rf $(BUILD_DIR) $(BINARY)
