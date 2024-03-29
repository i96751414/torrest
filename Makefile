CC = cc
CXX = c++
STRIP = strip

PROJECT = i96751414
NAME = torrest
GO_PKG = github.com/i96751414/torrest
GO = go
DOCKER = docker
LIBTORRENT_TAG = 1.2.14-0
UPX = upx
CGO_ENABLED = 1
BUILD_DIR = build
LIBTORRENT_GO = github.com/i96751414/libtorrent-go
GIT = git
GIT_VERSION = $(shell $(GIT) describe --tags | cut -c2-)
ifeq ($(GIT_VERSION),)
	GIT_VERSION := dev
endif

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
	GOPATH := $(shell go env GOPATH)
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

GO_LDFLAGS += -w -X $(GO_PKG)/util.Version=$(GIT_VERSION)

DOCKER_GOPATH = "/go"
DOCKER_WORKDIR = "$(DOCKER_GOPATH)/src/$(GO_PKG)"
DOCKER_GOCACHE = "/tmp/.cache"

WORKDIR = $(shell pwd)

OUTPUT_NAME = $(NAME)$(EXT)
BUILD_PATH = $(BUILD_DIR)/$(TARGET_OS)_$(TARGET_ARCH)
# LIBTORRENT_GO_HOME = "$(GOPATH)/src/$(LIBTORRENT_GO)"
LIBTORRENT_GO_HOME = "$$($(GO) list -m -f '{{.Dir}}' $(LIBTORRENT_GO))"

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

dependencies:
	$(GO) mod download
	chmod -R 755 $(LIBTORRENT_GO_HOME)

libtorrent-go: dependencies force
	$(MAKE) -C $(LIBTORRENT_GO_HOME) $(PLATFORM)

libtorrent-go-debug: dependencies force
	$(MAKE) -C $(LIBTORRENT_GO_HOME) debug PLATFORM=$(PLATFORM)

libtorrent-go-defines: dependencies force
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

vendor_darwin vendor_linux vendor_windows:

vendor_android:
	cp $(CROSS_ROOT)/sysroot/usr/lib/$(CROSS_TRIPLE)/libc++_shared.so $(BUILD_PATH)
	chmod +rx $(BUILD_PATH)/libc++_shared.so

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
	-v "$(WORKDIR)":$(DOCKER_WORKDIR) \
	-w $(DOCKER_WORKDIR) \
	$(PROJECT)/libtorrent-go-$(TARGET_OS)-$(TARGET_ARCH):$(LIBTORRENT_TAG) \
	make dist TARGET_OS=$(TARGET_OS) TARGET_ARCH=$(TARGET_ARCH) GIT_VERSION=$(GIT_VERSION)

docker: force
	$(DOCKER) run --rm -it \
	-e GOPATH=$(DOCKER_GOPATH) \
	-v "$(GOPATH)":$(DOCKER_GOPATH) \
	-v "$(WORKDIR)":$(DOCKER_WORKDIR) \
	-w $(DOCKER_WORKDIR) \
	$(PROJECT)/libtorrent-go-$(TARGET_OS)-$(TARGET_ARCH):$(LIBTORRENT_TAG) bash

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

pull-all:
	for i in $(PLATFORMS); do \
		$(MAKE) pull PLATFORM=$$i; \
	done

pull:
	$(DOCKER) pull $(PROJECT)/libtorrent-go-$(PLATFORM):$(LIBTORRENT_TAG)

binaries:
	@set -e; \
	for platform in $(PLATFORMS); do \
		$(MAKE) zip PLATFORM=$${platform}; \
	done

zip:
	cd $(BUILD_DIR) && mkdir -p binaries && \
	arch=$$(echo $(PLATFORM) | sed s/-/_/g) && \
	cd $${arch} && zip -9 -r ../binaries/$(NAME).$(GIT_VERSION).$${arch}.zip .

# go get -u github.com/swaggo/swag/cmd/swag
swag:
	swag init --generalInfo ./api/routes.go --output ./docs
