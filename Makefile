.PHONY: help \
	build run test tidy fmt vet \
	sidecar sidecar-macos-arm64 sidecar-macos-amd64 sidecar-windows-amd64 sidecar-linux-amd64 sidecar-linux-arm64 sidecar-all remote-sidecars \
	fe-install fe-dev fe-build \
	site-install site-dev site-check site-build \
	desktop-dev desktop-build \
	clean clean-sidecar

# ---- Paths and variables ----
BIN        := bin/foya
PKG        := ./...
KERNEL_PKG := ./cmd/foya
DESKTOP    := apps/desktop
SITE       := apps/site
SIDECAR_DIR:= $(DESKTOP)/src-tauri/binaries
REMOTE_DIR := $(SIDECAR_DIR)/remote
SIDECAR    := foya
SIDECAR_BUILD_FLAGS := -trimpath -ldflags="-s -w"

# Default target: print help.
help:
	@echo "Foya build commands:"
	@echo ""
	@echo "  Kernel (Go):"
	@echo "    make build              Build the kernel to $(BIN)"
	@echo "    make run                Build and start the kernel daemon"
	@echo "    make test / fmt / vet / tidy"
	@echo ""
	@echo "  Sidecar (cross-compiled kernel, named by target triple under $(SIDECAR_DIR)):"
	@echo "    make sidecar            Current platform only (for local desktop-dev/build)"
	@echo "    make sidecar-all        All platforms (for multi-platform releases)"
	@echo "    make remote-sidecars    Linux amd64/arm64 (for SSH auto-deploy)"
	@echo "    make sidecar-macos-arm64 / -macos-amd64 / -windows-amd64 / -linux-amd64"
	@echo ""
	@echo "  Frontend / Desktop (Tauri + Vue):"
	@echo "    make fe-install         Install frontend dependencies"
	@echo "    make fe-dev / fe-build  Frontend only"
	@echo "    make desktop-dev        Start the desktop app (includes kernel sidecar)"
	@echo "    make desktop-build      Build the desktop app installer"
	@echo ""
	@echo "  Website / Docs (Astro + Starlight):"
	@echo "    make site-install       Install website dependencies"
	@echo "    make site-dev           Start the website dev server"
	@echo "    make site-check         Check docs metadata, links, and pages"
	@echo "    make site-build         Build website static files"
	@echo ""
	@echo "    make clean / clean-sidecar"

# ---- Kernel (Go) ----
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

# ---- Sidecar cross-compilation ----
# Each platform uses Tauri's target triple naming; Tauri selects the current platform at bundle time.
sidecar-macos-arm64:
	GOOS=darwin GOARCH=arm64 go build $(SIDECAR_BUILD_FLAGS) -o $(SIDECAR_DIR)/$(SIDECAR)-aarch64-apple-darwin $(KERNEL_PKG)

sidecar-macos-amd64:
	GOOS=darwin GOARCH=amd64 go build $(SIDECAR_BUILD_FLAGS) -o $(SIDECAR_DIR)/$(SIDECAR)-x86_64-apple-darwin $(KERNEL_PKG)

sidecar-windows-amd64:
	GOOS=windows GOARCH=amd64 go build $(SIDECAR_BUILD_FLAGS) -o $(SIDECAR_DIR)/$(SIDECAR)-x86_64-pc-windows-msvc.exe $(KERNEL_PKG)

sidecar-linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(SIDECAR_BUILD_FLAGS) -o $(SIDECAR_DIR)/$(SIDECAR)-x86_64-unknown-linux-gnu $(KERNEL_PKG)

sidecar-linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(SIDECAR_BUILD_FLAGS) -o $(SIDECAR_DIR)/$(SIDECAR)-aarch64-unknown-linux-gnu $(KERNEL_PKG)

remote-sidecars:
	node $(DESKTOP)/scripts/build-remote-kernels.mjs

# Current-platform sidecar for local development and packaging. The target triple is resolved from go env.
sidecar:
	@mkdir -p $(SIDECAR_DIR)
	@GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH); \
	case "$$(go env GOOS)/$$(go env GOARCH)" in \
	  darwin/arm64)  T=aarch64-apple-darwin ;; \
	  darwin/amd64)  T=x86_64-apple-darwin ;; \
	  windows/amd64) T=x86_64-pc-windows-msvc.exe ;; \
	  linux/amd64)   T=x86_64-unknown-linux-gnu ;; \
	  linux/arm64)   T=aarch64-unknown-linux-gnu ;; \
	  *) echo "unsupported platform"; exit 1 ;; \
	esac; \
	go build $(SIDECAR_BUILD_FLAGS) -o $(SIDECAR_DIR)/$(SIDECAR)-$$T $(KERNEL_PKG); \
	echo "built sidecar: $(SIDECAR_DIR)/$(SIDECAR)-$$T"

# All-platform sidecars for multi-platform releases.
sidecar-all: sidecar-macos-arm64 sidecar-macos-amd64 sidecar-windows-amd64 sidecar-linux-amd64 sidecar-linux-arm64 remote-sidecars
	@echo "built all-platform sidecars in $(SIDECAR_DIR)"

# ---- Frontend / Desktop (Tauri + Vue) ----
fe-install:
	cd $(DESKTOP) && pnpm install

fe-dev:
	cd $(DESKTOP) && pnpm dev

fe-build:
	cd $(DESKTOP) && pnpm build

# Ensure the current-platform sidecar exists before desktop development or packaging.
desktop-dev: sidecar
	cd $(DESKTOP) && pnpm tauri dev

desktop-build: sidecar
	cd $(DESKTOP) && pnpm tauri build

# ---- Website / Docs (Astro + Starlight) ----
site-install:
	cd $(SITE) && pnpm install

site-dev:
	cd $(SITE) && pnpm start

site-check:
	cd $(SITE) && pnpm check

site-build:
	cd $(SITE) && pnpm build

# ---- Cleanup ----
clean:
	rm -rf bin dist

clean-sidecar:
	rm -f $(SIDECAR_DIR)/$(SIDECAR)-*
	rm -rf $(REMOTE_DIR)
