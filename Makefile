.PHONY: help \
	build run test tidy fmt vet \
	sidecar sidecar-macos-arm64 sidecar-macos-amd64 sidecar-windows-amd64 sidecar-linux-amd64 sidecar-all \
	fe-install fe-dev fe-build \
	desktop-dev desktop-build \
	clean clean-sidecar

# ---- 路径与变量 ----
BIN        := bin/foya
PKG        := ./...
KERNEL_PKG := ./cmd/foya
DESKTOP    := apps/desktop
SIDECAR_DIR:= $(DESKTOP)/src-tauri/binaries
SIDECAR    := foya

# 默认目标:打印帮助
help:
	@echo "Foya 构建命令:"
	@echo ""
	@echo "  内核 (Go):"
	@echo "    make build              编译内核到 $(BIN)"
	@echo "    make run                编译并启动内核 daemon"
	@echo "    make test / fmt / vet / tidy"
	@echo ""
	@echo "  Sidecar (交叉编译内核,按 target triple 命名到 $(SIDECAR_DIR)):"
	@echo "    make sidecar            仅当前平台 (供本机 desktop-dev/build)"
	@echo "    make sidecar-all        全平台 (供多平台发布)"
	@echo "    make sidecar-macos-arm64 / -macos-amd64 / -windows-amd64 / -linux-amd64"
	@echo ""
	@echo "  前端 / 桌面 (Tauri + Vue):"
	@echo "    make fe-install         安装前端依赖"
	@echo "    make fe-dev / fe-build  仅前端"
	@echo "    make desktop-dev        启动桌面 app (含内核 sidecar)"
	@echo "    make desktop-build      打包桌面 app 安装包"
	@echo ""
	@echo "    make clean / clean-sidecar"

# ---- 内核 (Go) ----
build:
	go build -o $(BIN) $(KERNEL_PKG)

run: build
	$(BIN)

test:
	go test $(PKG)

tidy:
	go mod tidy

fmt:
	go fmt $(PKG)

vet:
	go vet $(PKG)

# ---- Sidecar 交叉编译 ----
# 每个平台按 Tauri 约定的 target triple 命名,打包时 Tauri 自动挑当前平台那个。
sidecar-macos-arm64:
	GOOS=darwin GOARCH=arm64 go build -o $(SIDECAR_DIR)/$(SIDECAR)-aarch64-apple-darwin $(KERNEL_PKG)

sidecar-macos-amd64:
	GOOS=darwin GOARCH=amd64 go build -o $(SIDECAR_DIR)/$(SIDECAR)-x86_64-apple-darwin $(KERNEL_PKG)

sidecar-windows-amd64:
	GOOS=windows GOARCH=amd64 go build -o $(SIDECAR_DIR)/$(SIDECAR)-x86_64-pc-windows-msvc.exe $(KERNEL_PKG)

sidecar-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -o $(SIDECAR_DIR)/$(SIDECAR)-x86_64-unknown-linux-gnu $(KERNEL_PKG)

# 当前平台 sidecar (本机开发/打包用)。用 go env 解析当前 triple。
sidecar:
	@mkdir -p $(SIDECAR_DIR)
	@GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH); \
	case "$$(go env GOOS)/$$(go env GOARCH)" in \
	  darwin/arm64)  T=aarch64-apple-darwin ;; \
	  darwin/amd64)  T=x86_64-apple-darwin ;; \
	  windows/amd64) T=x86_64-pc-windows-msvc.exe ;; \
	  linux/amd64)   T=x86_64-unknown-linux-gnu ;; \
	  *) echo "unsupported platform"; exit 1 ;; \
	esac; \
	go build -o $(SIDECAR_DIR)/$(SIDECAR)-$$T $(KERNEL_PKG); \
	echo "built sidecar: $(SIDECAR_DIR)/$(SIDECAR)-$$T"

# 全平台 sidecar (多平台发布用)。
sidecar-all: sidecar-macos-arm64 sidecar-macos-amd64 sidecar-windows-amd64 sidecar-linux-amd64
	@echo "built all-platform sidecars in $(SIDECAR_DIR)"

# ---- 前端 / 桌面 (Tauri + Vue) ----
fe-install:
	cd $(DESKTOP) && pnpm install

fe-dev:
	cd $(DESKTOP) && pnpm dev

fe-build:
	cd $(DESKTOP) && pnpm build

# 桌面开发/打包前先确保当前平台 sidecar 就位。
desktop-dev: sidecar
	cd $(DESKTOP) && pnpm tauri dev

desktop-build: sidecar
	cd $(DESKTOP) && pnpm tauri build

# ---- 清理 ----
clean:
	rm -rf bin dist

clean-sidecar:
	rm -f $(SIDECAR_DIR)/$(SIDECAR)-*
