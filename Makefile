CC = cc
CXX = c++
STRIP = strip

PROJECT = i96751414
NAME = torrest
GO_PKG = github.com/i96751414/torrest
GO = go
GIT = git
DOCKER = docker
DOCKER_IMAGE = libtorrent-go
UPX = upx
GIT_VERSION = $(shell $(GIT) describe --tags)
CGO_ENABLED = 1
BUILD_DIR = build
LIBTORRENT_GO = github.com/i96751414/libtorrent-go
#GO_LDFLAGS += -w -X $(GO_PKG)/util.Version="$(GIT_VERSION)"
PLATFORMS = \
	android-arm \
	android-arm64 \
	android-x64 \
	android-x86 \
	darwin-x64 \
	linux-arm \
	linux-armv7 \
	linux-arm64 \
	linux-x64 \
	linux-x86 \
	windows-x64 \
	windows-x86

ifeq ($(GOPATH),)
	GOPATH = $(shell go env GOPATH)
endif

include platform_host.mk

ifneq ($(CROSS_TRIPLE),)
	CC := $(CROSS_TRIPLE)-$(CC)
	CXX := $(CROSS_TRIPLE)-$(CXX)
	STRIP := $(CROSS_TRIPLE)-strip
endif

include platform_target.mk

ifeq ($(TARGET_ARCH), x86)
	GOARCH = 386
else ifeq ($(TARGET_ARCH), x64)
	GOARCH = amd64
else ifeq ($(TARGET_ARCH), arm)
	GOARCH = arm
	GOARM = 6
else ifeq ($(TARGET_ARCH), armv7)
	GOARCH = arm
	GOARM = 7
	PKGDIR = -pkgdir $(GOPATH)/pkg/linux_armv7
else ifeq ($(TARGET_ARCH), arm64)
	GOARCH = arm64
	GOARM =
endif

ifeq ($(TARGET_OS), windows)
	EXT = .exe
	GOOS = windows
	# TODO Remove for golang 1.8
	# https://github.com/golang/go/issues/8756
	GO_LDFLAGS = -extldflags=-Wl,--allow-multiple-definition -v
else ifeq ($(TARGET_OS), darwin)
	EXT =
	GOOS = darwin
	# Needs this or cgo will try to link with libgcc, which will fail
	CC := $(CROSS_ROOT)/bin/$(CROSS_TRIPLE)-clang
	CXX := $(CROSS_ROOT)/bin/$(CROSS_TRIPLE)-clang++
	GO_LDFLAGS = -linkmode=external -extld=$(CC)
else ifeq ($(TARGET_OS), linux)
	EXT =
	GOOS = linux
	GO_LDFLAGS = -linkmode=external -extld=$(CC)
else ifeq ($(TARGET_OS), android)
	EXT =
	GOOS = android
	ifeq ($(TARGET_ARCH), arm)
		GOARM = 7
	else
		GOARM =
	endif
	GO_LDFLAGS = -linkmode=external -extldflags=-pie -extld=$(CC)
	CC := $(CROSS_ROOT)/bin/$(CROSS_TRIPLE)-clang
	CXX := $(CROSS_ROOT)/bin/$(CROSS_TRIPLE)-clang++
endif

DOCKER_GOPATH = "/go"
DOCKER_WORKDIR = "$(DOCKER_GOPATH)/src/$(GO_PKG)"
DOCKER_GOCACHE = "/tmp/.cache"

OUTPUT_NAME = $(NAME)$(EXT)
BUILD_PATH = $(BUILD_DIR)/$(TARGET_OS)_$(TARGET_ARCH)
LIBTORRENT_GO_HOME = "$(GOPATH)/src/$(LIBTORRENT_GO)"

USERGRP = "$(shell id -u):$(shell id -g)"

.PHONY: $(PLATFORMS)

all:
	for i in $(PLATFORMS); do \
		$(MAKE) $$i; \
	done

$(PLATFORMS):
	$(MAKE) build TARGET_OS=$(firstword $(subst -, ,$@)) TARGET_ARCH=$(word 2, $(subst -, ,$@))

force:
	@true

libtorrent-go: force
	$(MAKE) -C $(LIBTORRENT_GO_HOME) $(PLATFORM)

libtorrent-go-defines:
	$(MAKE) -C $(LIBTORRENT_GO_HOME) defines

$(BUILD_PATH):
	mkdir -p $(BUILD_PATH)

$(BUILD_PATH)/$(OUTPUT_NAME): libtorrent-go-defines $(BUILD_PATH) force
	export LDFLAGS='$(LDFLAGS)'; \
	export CC='$(CC)'; \
	export CXX='$(CXX)'; \
	export GOOS='$(GOOS)'; \
	export GOARCH='$(GOARCH)'; \
	export GOARM='$(GOARM)'; \
	export CGO_ENABLED='$(CGO_ENABLED)'; \
	$(GO) build -v \
		-gcflags '$(GO_GCFLAGS)' \
		-ldflags '$(GO_LDFLAGS)' \
		-o '$(BUILD_PATH)/$(OUTPUT_NAME)' \
		$(PKGDIR) && \
	set -x && \
	$(GO) vet -unsafeptr=false .

vendor_darwin vendor_linux:

vendor_windows:
	find "$(GOPATH)/pkg/$(GOOS)_$(GOARCH)" -name *.dll -exec cp -f {} $(BUILD_PATH) \;

vendor_android:
	cp $(CROSS_ROOT)/sysroot/usr/lib/$(CROSS_TRIPLE)/libc++_shared.so $(BUILD_PATH)
	chmod +rx $(BUILD_PATH)/libc++_shared.so

vendor_libs_windows:

vendor_libs_android:
	$(CROSS_ROOT)/sysroot/usr/lib/$(CROSS_TRIPLE)/libc++_shared.so

torrest: $(BUILD_PATH)/$(OUTPUT_NAME)

re: clean build

clean:
	rm -rf $(BUILD_PATH)

distclean:
	rm -rf $(BUILD_DIR)

build: force
	$(DOCKER) run --rm \
	-u $(USERGRP) \
	-e GOPATH=$(DOCKER_GOPATH) \
	-e GOCACHE=$(DOCKER_GOCACHE) \
	-v "$(GOPATH)":$(DOCKER_GOPATH) \
	-v "$(shell pwd)":$(DOCKER_WORKDIR) \
	-w $(DOCKER_WORKDIR) \
	$(DOCKER_IMAGE):$(TARGET_OS)-$(TARGET_ARCH) \
	make dist TARGET_OS=$(TARGET_OS) TARGET_ARCH=$(TARGET_ARCH) GIT_VERSION=$(GIT_VERSION)

docker: force
	$(DOCKER) run --rm \
	-e GOPATH=$(DOCKER_GOPATH) \
	-v "$(GOPATH)":$(DOCKER_GOPATH) \
	-v "$(shell pwd)":$(DOCKER_WORKDIR) \
	-w $(DOCKER_WORKDIR) \
	$(DOCKER_IMAGE):$(TARGET_OS)-$(TARGET_ARCH)

strip: force
	@find $(BUILD_PATH) -type f ! -name "*.exe" -exec $(STRIP) {} \;

upx: force
# Do not .exe files, as upx doesn't really work with 8l/6l linked files.
# It's fine for other platforms, because we link with an external linker, namely
# GCC or Clang. However, on Windows this feature is not yet supported.
	@find $(BUILD_PATH) -type f ! -name "*.exe" -a ! -name "*.so" -exec $(UPX) --lzma {} \;

checksum: $(BUILD_PATH)/$(OUTPUT_NAME)
	shasum -b $(BUILD_PATH)/$(OUTPUT_NAME) | cut -d' ' -f1 >> $(BUILD_PATH)/$(OUTPUT_NAME)

dist: torrest vendor_$(TARGET_OS) strip checksum

libs: force
	$(MAKE) libtorrent-go PLATFORM=$(PLATFORM)

pull-all:
	for i in $(PLATFORMS); do \
		docker pull $(PROJECT)/libtorrent-go:$$i; \
		docker tag $(PROJECT)/libtorrent-go:$$i libtorrent-go:$$i; \
	done

pull:
	docker pull $(PROJECT)/libtorrent-go:$(PLATFORM)
	docker tag $(PROJECT)/libtorrent-go:$(PLATFORM) libtorrent-go:$(PLATFORM)

# go get -u github.com/swaggo/swag/cmd/swag
swag:
	swag init -g ./api/routes.go -o ./docs
