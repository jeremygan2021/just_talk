.PHONY: build install install-desktop uninstall-desktop icons test clean run

APP_NAME := just-talk
CMD_DIR := ./cmd/just-talk
BUILD_DIR := ./build
ICON_SCRIPT := scripts/install-desktop.sh

# Build for current platform
build:
	go build -o $(BUILD_DIR)/$(APP_NAME) $(CMD_DIR)

# Generate the .desktop launcher icon (mic glyph PNG).
icons:
	@command -v convert >/dev/null 2>&1 || { echo "ImageMagick is required: sudo apt install imagemagick"; exit 1; }
	mkdir -p $(BUILD_DIR)
	convert -size 256x256 xc:none \
		-fill "#4F46E5" -draw "roundrectangle 80,40 176,180 48,48" \
		-fill "#4F46E5" -draw "rectangle 124,180 132,232" \
		-fill "#4F46E5" -draw "roundrectangle 80,220 176,244 12,12" \
		-fill "white" -stroke "white" -strokewidth 2 \
		-draw "line 60,160 60,180" \
		-draw "line 196,160 196,180" \
		-stroke "none" \
		-fill "#4F46E5" -draw "roundrectangle 56,156 200,184 8,8" \
		-fill "white" -draw "rectangle 122,40 134,150" \
		-fill "white" -draw "circle 128,90 128,70" \
		-fill "#4F46E5" -draw "rectangle 90,60 166,72" \
		$(BUILD_DIR)/$(APP_NAME).png

# Install to ~/.local/bin
install: build
	$(BUILD_DIR)/$(APP_NAME) --install

# Install .desktop file + icon (launcher integration).
install-desktop: build icons
	$(ICON_SCRIPT) install

# Remove .desktop file + icon.
uninstall-desktop:
	$(ICON_SCRIPT) uninstall

# Run (current platform)
run:
	go run $(CMD_DIR)

# Test
test:
	go test ./... -v

# Clean
clean:
	rm -rf $(BUILD_DIR)

# Install dependencies
deps:
	go mod tidy
	go mod download
