BINARY     = localvoice
INSTALL    = $(HOME)/.local/bin/localvoice
PLIST_SRC  = com.localvoiceai.localvoice.plist
PLIST_DEST = $(HOME)/Library/LaunchAgents/com.localvoiceai.localvoice.plist
LABEL      = com.localvoiceai.localvoice

CGO_CFLAGS  = -I/opt/homebrew/include
CGO_LDFLAGS = -L/opt/homebrew/lib \
              -lwhisper -lggml -lggml-base -lggml-cpu -lggml-metal -lggml-blas \
              -framework Accelerate -framework Metal -framework Foundation -framework CoreGraphics

.PHONY: build install update reload uninstall start stop status clean setup

build:
	CGO_CFLAGS="$(CGO_CFLAGS)" \
	  go build -o $(BINARY) ./cmd/localvoice/
	codesign -s - --force $(BINARY)
	@echo "Built: $(BINARY)"

install: build
	@echo "Installing to $(INSTALL)..."
	@mkdir -p $(HOME)/.local/bin
	cp $(BINARY) $(INSTALL)
	codesign -s - --force $(INSTALL)
	@mkdir -p $(HOME)/Library/LaunchAgents
	sed "s|INSTALL_PATH|$(INSTALL)|g" $(PLIST_SRC) > $(PLIST_DEST)
	launchctl unload $(PLIST_DEST) 2>/dev/null || true
	launchctl load $(PLIST_DEST)
	@echo ""
	@echo "Installed. Grant permissions once (they persist across reboots):"
	@echo "  System Settings → Privacy & Security → Accessibility    → add $(INSTALL)"
	@echo "  System Settings → Privacy & Security → Input Monitoring → add $(INSTALL)"
	@echo ""
	@echo "Then: make start"

update: build
	@echo "Updating binary at $(INSTALL)..."
	launchctl stop $(LABEL) 2>/dev/null || true
	cp $(BINARY) $(INSTALL)
	codesign -s - --force $(INSTALL)
	launchctl start $(LABEL)
	@echo "Updated. NOTE: re-grant Accessibility + Input Monitoring permissions (binary hash changed)."

reload:
	@echo "Reloading agent (plist only, no binary change)..."
	launchctl stop $(LABEL) 2>/dev/null || true
	launchctl unload $(PLIST_DEST) 2>/dev/null || true
	sed "s|INSTALL_PATH|$(INSTALL)|g" $(PLIST_SRC) > $(PLIST_DEST)
	launchctl load $(PLIST_DEST)
	launchctl start $(LABEL)
	@echo "Reloaded. No permission re-grant needed."

uninstall:
	launchctl unload $(PLIST_DEST) 2>/dev/null || true
	rm -f $(PLIST_DEST) $(INSTALL)
	@echo "Uninstalled."

start:
	launchctl start $(LABEL)
	@echo "LocalVoiceAI started. Logs: tail -f /tmp/localvoice.log"

stop:
	launchctl stop $(LABEL)
	@echo "LocalVoiceAI stopped."

status:
	@launchctl list | grep $(LABEL) || echo "Not running."

setup:
	@echo "Installing dependencies..."
	brew install pkg-config portaudio whisper-cpp
	@ln -sf /opt/homebrew/lib/libggml.dylib /opt/homebrew/lib/libggml-cpu.dylib  2>/dev/null || true
	@ln -sf /opt/homebrew/lib/libggml.dylib /opt/homebrew/lib/libggml-metal.dylib 2>/dev/null || true
	@ln -sf /opt/homebrew/lib/libggml.dylib /opt/homebrew/lib/libggml-blas.dylib  2>/dev/null || true
	@echo "Done. Run 'make install' to build and start the service."

clean:
	rm -f $(BINARY)
