BINARY      := sqwee
INSTALL_DIR := $(HOME)/.local/bin
BUILD_DIR   := bin
RELEASES_DIR := releases
COMMIT      := $(shell git rev-parse HEAD 2>/dev/null || printf dev)

.PHONY: all build build-all install uninstall clean run

all: build

build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w -X main.Commit=$(COMMIT)" -o $(BUILD_DIR)/$(BINARY) .
	@echo "Built: $(BUILD_DIR)/$(BINARY)"

build-all:
	@mkdir -p $(RELEASES_DIR)/linux/amd64 $(RELEASES_DIR)/linux/arm64 $(RELEASES_DIR)/darwin/amd64 $(RELEASES_DIR)/darwin/arm64 $(RELEASES_DIR)/windows
	@echo "Building linux/amd64..."
	GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w -X main.Commit=$(COMMIT)" -o $(RELEASES_DIR)/linux/amd64/$(BINARY) .
	@echo "Building linux/arm64..."
	GOOS=linux   GOARCH=arm64 go build -ldflags="-s -w -X main.Commit=$(COMMIT)" -o $(RELEASES_DIR)/linux/arm64/$(BINARY) .
	@echo "Building darwin/amd64..."
	GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w -X main.Commit=$(COMMIT)" -o $(RELEASES_DIR)/darwin/amd64/$(BINARY) .
	@echo "Building darwin/arm64..."
	GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w -X main.Commit=$(COMMIT)" -o $(RELEASES_DIR)/darwin/arm64/$(BINARY) .
	@echo "Building windows/amd64..."
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X main.Commit=$(COMMIT)" -o $(RELEASES_DIR)/windows/$(BINARY).exe .
	@echo ""
	@echo "Pre-built binaries written to $(RELEASES_DIR)/"
	@echo "Commit these files so users without Go can install without building."

run:
	go run .

install:
	@mkdir -p $(INSTALL_DIR)
	@if command -v go >/dev/null 2>&1; then \
		echo "==> Go found - building sqwee from source..."; \
		mkdir -p $(BUILD_DIR); \
		go build -ldflags="-s -w -X main.Commit=$(COMMIT)" -o $(BUILD_DIR)/$(BINARY) . || exit 1; \
		cp $(BUILD_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY); \
		echo "    Built and installed from source."; \
	else \
		echo "==> Go not found - installing pre-built binary from releases/..."; \
		OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
		ARCH=$$(uname -m); \
		case "$$ARCH" in \
			x86_64|amd64) ARCH=amd64 ;; \
			aarch64|arm64) ARCH=arm64 ;; \
			*) echo "ERROR: Unsupported architecture: $$ARCH"; exit 1 ;; \
		esac; \
		if [ "$$OS" = "darwin" ]; then \
			RELEASE_BIN="$(RELEASES_DIR)/darwin/$$ARCH/$(BINARY)"; \
		elif [ "$$OS" = "linux" ]; then \
			RELEASE_BIN="$(RELEASES_DIR)/linux/$$ARCH/$(BINARY)"; \
		else \
			echo "ERROR: Unsupported OS: $$OS"; \
			exit 1; \
		fi; \
		if [ ! -f "$$RELEASE_BIN" ]; then \
			echo "ERROR: Pre-built binary not found at $$RELEASE_BIN"; \
			echo "       Please install Go (https://go.dev/dl/) and re-run, or ask a developer to run 'make build-all' and commit the releases/ folder."; \
			exit 1; \
		fi; \
		cp "$$RELEASE_BIN" $(INSTALL_DIR)/$(BINARY); \
		chmod +x $(INSTALL_DIR)/$(BINARY); \
		echo "    Installed pre-built binary."; \
	fi
	@echo ""
	@echo "  sqwee installed to $(INSTALL_DIR)/$(BINARY)"
	@echo ""
	@if echo "$$PATH" | grep -q "$(INSTALL_DIR)"; then \
		echo "  $(INSTALL_DIR) is already in your PATH."; \
	else \
		echo "  NOTE: $(INSTALL_DIR) is not in your PATH."; \
		echo "  Add this to your shell rc file and reload:"; \
		echo "    export PATH=\"\$$HOME/.local/bin:\$$PATH\""; \
	fi
	@echo ""
	@echo "  Config file (created on first launch):"
	@echo "    Linux:  \$$HOME/.config/delbysoft/sqwee.toml"
	@echo "    macOS:  \$$HOME/Library/Application Support/delbysoft/sqwee.toml"
	@echo ""
	@echo "  Data file (saved connections):"
	@echo "    Linux:  \$$HOME/.config/delbysoft/sqwee.json"
	@echo "    macOS:  \$$HOME/Library/Application Support/delbysoft/sqwee.json"
	@echo ""
	@echo "  Run: sqwee"

uninstall:
	@rm -f $(INSTALL_DIR)/$(BINARY)
	@echo "Removed $(INSTALL_DIR)/$(BINARY)"
	@echo ""
	@echo "Config and data files have been left in place."
	@echo "To fully remove, delete:"
	@echo "  Linux:  \$$HOME/.config/delbysoft/"
	@echo "  macOS:  \$$HOME/Library/Application Support/delbysoft/"

clean:
	rm -rf $(BUILD_DIR)
