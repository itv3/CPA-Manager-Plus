# Manager SQLite 备份与恢复

CPA Manager Plus 使用同一个 SQLite 数据库保存统一账号 UUID、绑定历史、操作草稿、用量事件和管理配置。数据库默认位于 `/data/usage.sqlite`，备份默认位于 `/data/backups`，两者都应放在持久化卷中。

## 自动备份

Manager Server 默认启用以下保护：

- 数据库 schema 版本低于当前程序版本时，先创建 `pre_migration` 一致性快照，再执行迁移。
- 服务启动后立即创建一次 `periodic` 快照，之后默认每 24 小时执行一次。
- 每种备份类型独立执行保留策略，默认保留最近 14 份；手工备份不会挤占定期备份名额。
- 快照使用 SQLite `VACUUM INTO`，会把主文件和 WAL 中已经提交的数据合并为一个独立数据库文件。
- 数据库文件完成 `quick_check`、schema 和必需表校验后才发布；清单最后原子写入，因此不完整快照不会出现在可恢复备份列表中。

可通过环境变量调整：

```text
CPA_MANAGER_BACKUP_ENABLED=true
CPA_MANAGER_BACKUP_DIR=/data/backups
CPA_MANAGER_BACKUP_INTERVAL_HOURS=24
CPA_MANAGER_BACKUP_RETENTION=14
```

每份备份包含：

```text
usage-<UTC时间>-schema-v<版本>-<类型>-<随机标识>.sqlite
usage-<UTC时间>-schema-v<版本>-<类型>-<随机标识>.sqlite.manifest.json
```

清单记录备份格式版本、SQLite schema 版本、创建时间、文件大小和 SHA256。恢复时必须同时保留数据库文件和清单。

## 手工备份与校验

容器运行时可以创建在线一致性备份：

```bash
docker compose exec cpa-manager-plus \
  cpa-manager-plus backup-db \
  --db-path /data/usage.sqlite \
  --backup-dir /data/backups
```

恢复前先校验：

```bash
docker compose exec cpa-manager-plus \
  cpa-manager-plus verify-db-backup \
  --backup /data/backups/usage-xxxx.sqlite
```

校验内容包括清单格式、SHA256、文件大小、`quick_check`、schema 版本和 Manager 必需表。

## 离线恢复

恢复必须在 Manager Server 已停止时执行。不要在运行中的容器内替换 SQLite；旧连接可能继续向已被替换的文件写入，造成恢复后的数据丢失。

推荐流程：

```bash
docker compose stop cpa-manager-plus

docker compose run --rm --no-deps cpa-manager-plus \
  restore-db \
  --backup /data/backups/usage-xxxx.sqlite \
  --db-path /data/usage.sqlite \
  --backup-dir /data/backups \
  --confirm-stopped

docker compose up -d cpa-manager-plus
```

恢复命令会按以下顺序执行：

1. 校验来源备份。
2. 为当前数据库创建 `pre_restore` 回滚备份。
3. 把来源备份复制到目标目录的临时文件并再次校验。
4. 清理旧 WAL、SHM 和 journal 边车文件。
5. 在同一文件系统内替换目标数据库并同步目录。
6. 下次启动时按 schema 版本执行必要迁移。

启动后应检查 `/health`、`/status`、统一账号列表、绑定历史、未完成草稿和用量统计。如果验收失败，停止服务并用命令输出中的 `pre_restore` 备份恢复。

## 数据密钥

SQLite 中的 CPA Management Key 使用 `data.key` 加密。数据库备份不会把 `data.key` 打包到同一个文件，避免单个备份同时包含密文和解密密钥。

完整灾备必须分别保护：

```text
/data/usage.sqlite 及其版本化备份
/data/data.key
```

不要把 `data.key` 上传到工单、公开仓库或与数据库备份放在同一个公开下载位置。
