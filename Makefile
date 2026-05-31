BINARY      := sqwee
INSTALL_DIR := $(HOME)/.local/bin
BUILD_DIR   := bin
COMMIT      := $(shell git rev-parse HEAD 2>/dev/null || printf dev)

.PHONY: all build install uninstall clean run

all: build

build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w -X main.Commit=$(COMMIT)" -o $(BUILD_DIR)/$(BINARY) .
	@echo "Built: $(BUILD_DIR)/$(BINARY)"

run:
	go run .

install: build
	@mkdir -p $(INSTALL_DIR)
	cp $(BUILD_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)
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
