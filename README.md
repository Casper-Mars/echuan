# echuan

驿传（echuan）：单二进制 Web 服务，前端通过 `go:embed` 打包，SQLite 持久化。

## 本地开发

```bash
make build      # 编译 ./echuan
make test       # go test ./... -race
make web        # 构建前端并同步到 internal/web/dist
make lint
```

## Docker 部署

```bash
cp .env.example .env          # 可选：自定义数据目录 / 端口
make docker-build             # 多阶段构建（前端 + Go + 最小运行镜像）
make docker-up                # docker compose up -d
curl -fsS 127.0.0.1:8720/readyz
```

- 数据默认落在 `~/echuan-data`（可用 `.env` 的 `ECHUAN_DATA_DIR` 覆盖），删容器/换镜像不丢数据。
- 容器以 UID/GID `10001` 非 root 运行，仅监听 `127.0.0.1:8720`。
- 备份：`scripts/backup.sh`；恢复：`scripts/restore.sh --latest`。

完整的首次部署、配置、备份恢复、升级、排障与上线 curl 手册见 **[docs/deploy.md](docs/deploy.md)**。
