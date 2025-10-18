BINARY := datum-mcp
CMD_PKG := ./cmd/datum-mcp
DIST := dist

GO ?= go
CGO_ENABLED ?= 0
LDFLAGS ?=

# Optional extension for Windows targets
EXTENSION :=
ifeq ($(GOOS),windows)
EXTENSION := .exe
endif

.PHONY: all build clean build-all build-target

all: build

build:
	@mkdir -p $(DIST)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY) $(CMD_PKG)

clean:
	rm -rf $(DIST)

# Build for a specific target: make build-target GOOS=darwin GOARCH=arm64
build-target:
	@test -n "$(GOOS)" || (echo "GOOS is required" && exit 1)
	@test -n "$(GOARCH)" || (echo "GOARCH is required" && exit 1)
	@mkdir -p $(DIST)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY)_$(GOOS)_$(GOARCH)$(EXTENSION) $(CMD_PKG)

# Convenience: build common targets locally
build-all: clean
	$(MAKE) build-target GOOS=darwin GOARCH=arm64
	$(MAKE) build-target GOOS=darwin GOARCH=amd64
    $(MAKE) build-target GOOS=linux GOARCH=arm64
	$(MAKE) build-target GOOS=linux GOARCH=amd64
	$(MAKE) build-target GOOS=windows GOARCH=amd64
	$(MAKE) build-target GOOS=windows GOARCH=arm64


