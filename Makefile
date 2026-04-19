# Usage:
#   make              - build for current platform
#   make all          - build for all target platforms
#   make linux-arm64  - build for Linux ARM64
#   make linux-armv7  - build for Linux ARMv7
#   make clean        - remove build artifacts

BINARY := virtual_bess
BUILD_DIR := build

.PHONY: all clean linux-amd64 linux-arm64 linux-armv7

all: linux-amd64 linux-arm64 linux-armv7

$(BINARY):
	go build -o $(BINARY) .

linux-amd64:
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-linux-amd64 .

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(BINARY)-linux-arm64 .

linux-armv7:
	GOOS=linux GOARCH=arm GOARM=7 go build -o $(BUILD_DIR)/$(BINARY)-linux-armv7 .

clean:
	rm -rf $(BUILD_DIR) $(BINARY)
