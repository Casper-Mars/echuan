# echuan — 单二进制 Web 服务
#
# 常用目标：build / test / lint / web
BINARY  := echuan
PKG     := github.com/Casper-Mars/echuan
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DOCKER_IMAGE ?= echuan:local
LDFLAGS := -s -w \
	-X $(PKG)/internal/platform.Version=$(VERSION) \
	-X $(PKG)/internal/platform.Commit=$(COMMIT)

.PHONY: all build test lint fmt web contract clean \
	docker-build docker-up docker-down docker-logs docker-size docker-clean

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

## docker-build: 构建镜像；收尾自动清理 build cache 与本项目悬空镜像
docker-build:
	ECHUAN_VERSION=$(VERSION) ECHUAN_COMMIT=$(COMMIT) docker compose build
	@sleep 1  # 等 BuildKit 写完缓存记录再清理，确保 build cache 归零
	@docker builder prune -f >/dev/null
	# Docker 不会删除仍被容器引用的镜像（例如其他项目的 mysql），因此可安全全量清理悬空镜像。
	@docker image prune -f >/dev/null
	@docker image inspect $(DOCKER_IMAGE) \
		--format 'image: {{index .RepoTags 0}} size={{.Size}} bytes id={{.Id}}'

## docker-up: 启动服务（不重新构建；需要新镜像先 make docker-build）
docker-up:
	docker compose up -d
	@docker compose ps

## docker-down: 停止并删除容器（数据目录保留）
docker-down:
	docker compose down

## docker-logs: 跟踪容器日志
docker-logs:
	docker compose logs -f --tail=100

## docker-size: 打印运行镜像体积
docker-size:
	@docker image inspect $(DOCKER_IMAGE) \
		--format '{{index .RepoTags 0}}: {{.Size}} bytes'

## docker-clean: 删除本项目容器/镜像并清理构建缓存（不删除宿主机数据）
docker-clean:
	-docker compose down --remove-orphans
	-docker image rm -f $(DOCKER_IMAGE)
	docker builder prune -f
	@docker image prune -f
