# 事故记录：sub2api 崩溃循环（2026-09-19）

- 状态：已恢复（回滚镜像）
- 影响：252 服务器（192.168.31.252）sub2api 容器停机，约 07:20–07:48（+0800）
- 涉及镜像：`zjarlin/sub2api:0.1.183-custom-854bd12cb-generic400chatfallback-20260919`

## 症状

容器进入崩溃循环（`docker ps` 显示 `Restarting (1)`，约每 60 秒重启一次），每次启动日志固定报错：

```
server/main.go:94 Failed to initialize application:
apply migration ._001_init.sql: pq: invalid message format
```

`restart: unless-stopped` 策略使进程无限重启，服务整体不可用。

## 根因

1. 2026-09-19 00:20 部署了新镜像 `...generic400chatfallback-20260919`（`/opt/sub2api/docker-compose.override.yml` 于 00:21 更新）。
2. 该镜像的 Go 二进制**嵌入了 macOS 垃圾文件 `._001_init.sql`**。`._` 开头是 macOS AppleDouble 元数据文件（Finder/zip 在 macOS 上产生），构建机 `backend/migrations/` 工作树中存在该**未跟踪**文件。
3. 嵌入路径：`backend/migrations/migrations.go:33` 使用 `//go:embed *.sql`。Go embed 的**文件 glob 模式不会排除点开头文件**（只有目录式 embed 才排除 `.`/`_` 开头文件），因此 `._001_init.sql` 被打进二进制。
4. 执行路径：`backend/internal/repository/migrations_runner.go:161` 使用 `fs.Glob(fsys, "*.sql")` 枚举迁移，Go 的 `path.Match` 中 `*` **会匹配点开头文件**（与 shell glob 不同）；文件名排序时 `._001_init.sql` 排在 `001_init.sql` 之前，被第一个当作迁移执行。文件内容是 AppleDouble 二进制乱码，PostgreSQL 协议解析失败，返回 `pq: invalid message format`，应用退出（exit 1）。

## 证据

| 检查项 | 结果 |
|---|---|
| 新镜像二进制内 `grep -ac "._001_init" /app/sub2api` | 1（命中） |
| 旧镜像 `live-models-617b51f191` 同样检查 | 0（干净） |
| `sub2api-postgres`（postgres:15-alpine3.20）状态 | Up 2 个月，healthy |
| 同网络实测 `pg_isready -h postgres` | accepting connections |
| 同网络 `psql` 错误密码 | FATAL: password authentication failed（标准 PG 协议响应，说明库端正常） |
| 服务器构建源 `backend/migrations/`（292 个迁移） | 无 `._` 文件（垃圾文件只在构建机工作树，未提交） |

结论：数据库与网络正常，问题 100% 在新镜像内嵌迁移文件。

## 处置（已执行）

1. 备份当前 override：`docker-compose.override.yml.bak-rollback-20260919`
2. override 中 sub2api 镜像回退为上一个正常版本 `zjarlin/sub2api:0.1.183-custom-854bd12cb-live-models-617b51f191`
3. `cd /opt/sub2api && docker compose up -d sub2api`
4. 验证：容器 `Up (healthy)`，日志正常启动（定价 239 模型、网关 bootstrap），迁移不再执行失败

## 根治建议

### 1. 代码层（防回归，✅ 已实施，提交 d69c9a91b）

`backend/internal/repository/migrations_runner.go` 新增 `listMigrationFiles()`，枚举迁移后跳过点/下划线开头文件，并用于 `applyMigrationsFS` 与 `latestMigrationBaseline` 两处；新增回归测试 `TestApplyMigrationsFS_SkipDotAndUnderscorePrefixedFiles`、`TestLatestMigrationBaseline_SkipsDotAndUnderscoreFiles`（`go test ./internal/repository/` 通过）。

### 2. 构建层（待执行）

- 构建前清理：`find backend/migrations -name "._*" -delete`（及任何 embed 目录）
- 或把 embed 改为目录形式（`//go:embed migrations`，目录 embed 天然排除 `.`/`_` 开头文件）——需调整包路径与 `migrations.FS`
- macOS 打包/传输用排除 `._*` 的 tar：`COPYFILE_DISABLE=1 tar ...` 或 `--exclude='._*'`

### 3. 检查

- 全仓库（含未跟踪文件）排查 `._*`：`find . -name "._*" -not -path "./.git/*"`
- 后续构建镜像后抽验二进制：`docker run --rm --entrypoint grep <image> -ac "._001_init" /app/sub2api` 应返回 0

## 相关文件

- 部署：`/opt/sub2api/docker-compose.yml`、`docker-compose.override.yml`
- 嵌入：`backend/migrations/migrations.go`（`//go:embed *.sql`）
- 执行：`backend/internal/repository/migrations_runner.go`（`fs.Glob` + 排序 + 应用）
