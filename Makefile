# echuan — 单二进制 Web 服务
#
# 常用目标：build / test / lint / web
BINARY  := echuan
PKG     := github.com/Casper-Mars/echuan
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w \
	-X $(PKG)/internal/platform.Version=$(VERSION) \
	-X $(PKG)/internal/platform.Commit=$(COMMIT)

.PHONY: all build test lint fmt web contract clean

all: build

## build: 编译单文件二进制（CGO 关闭、去除路径与符号）
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/echuan
	@echo "built ./$(BINARY) ($(VERSION) @ $(COMMIT))"

## test: 全量单测（含竞态检测）
test:
	go test ./... -race

## lint: go vet，若存在 staticcheck 则一并执行（未安装时跳过，不失败）
lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck ./...; \
	else \
		echo "staticcheck 未安装，跳过（安装：go install honnef.co/go/tools/cmd/staticcheck@latest）"; \
	fi

## contract: 接口契约校验（28 个 operation + 校验器自测）
contract:
	go test ./internal/contract -count=1

## fmt: 格式化代码
fmt:
	gofmt -w .

## web: 构建前端并同步到 internal/web/dist（前端源码由 Task-14 创建）
web:
	@if [ -f web/package.json ]; then \
		pnpm --dir web build && rm -rf internal/web/dist && cp -r web/dist internal/web/dist && echo "前端产物已同步到 internal/web/dist"; \
	else \
		echo "web/ 尚未创建（由 Task-14 提供），保留 internal/web/dist 占位页"; \
	fi

## clean: 清理构建产物
clean:
	rm -f $(BINARY)
