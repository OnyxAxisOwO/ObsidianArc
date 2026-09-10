# 备份与迁移

完整实例备份需要数据库和实例密钥。个人导出只包含自己的对话与偏好，不能替代实例备份。

## 数据保存在哪里

默认 SQLite 模式：

```text
data/
├── obsidian.db
├── obsidian.db-wal
├── obsidian.db-shm
└── secret.key
```

WAL 和 SHM 文件可能只在数据库运行期间存在。用户、模型、会话、账本和仍保留的附件二进制都在数据库中，应用没有独立的 `uploads/` 文件目录。

如果设置了 `OBSIDIAN_DB_DSN`，SQLite 文件可能位于其他位置。如果使用 `OBSIDIAN_SECRET_KEY`，需另外保存该环境变量的值，而不是依赖自动生成的文件。

## 备份 SQLite

最直接的方法是停止写入后备份整个数据目录。以下对应安装页的 Docker 容器，在 Bash 中执行：

```bash
docker stop obsidian-arc
docker cp obsidian-arc:/data "./obsidian-arc-backup-$(date -u +%Y%m%dT%H%M%SZ)"
docker start obsidian-arc
```

复制操作成功后检查备份目录，并将它保存到其他存储位置。失败时保留错误信息，确认服务已经重新启动。

需要不停机备份时，使用 SQLite 的一致性备份工具。不要在运行中只复制 `obsidian.db` 并忽略 WAL。当前管理后台没有“下载数据库热备份”功能。

## 备份 PostgreSQL

使用 PostgreSQL 的备份工具。仓库 Compose 中数据库服务名是 `db`，默认用户和数据库名均为 `obsidian`：

```bash
docker compose exec -T db pg_dump -U obsidian -d obsidian -Fc > obsidian-arc.dump
```

上述重定向示例用于 Bash。自定义过用户名或数据库名时替换对应参数。

附件数据包含在数据库备份中。实例密钥和部署配置仍需单独保存；它们不由 `pg_dump` 自动备份。

## 恢复实例

先在隔离环境验证备份，再替换正在使用的数据：

1. 停止应用写入。
2. 恢复数据库，并使用备份时的实例密钥。
3. 检查数据库或数据目录权限。
4. 启动匹配版本的程序，检查迁移和启动日志。
5. 验证登录、历史对话、保留的附件与一次模型调用。

不要把备份解压到正在写入的数据库上。升级后的备份也不一定适用于旧程序版本。

## 导出与导入个人数据

个人数据接口为 `GET /api/account/export` 和 `POST /api/account/import`，需要登录。

导出包含对话文字、推理内容和偏好设置。图片只记录数量，不包含图片二进制；服务商、API 密钥、其他账号和实例账本也不在导出范围内。

导入会追加会话，不覆盖现有历史。重复导入可能产生多份内容。当前单份导入上限为 32 MB、2,000 个会话和 50,000 条消息，同时受账号总存储上限约束。

## 更换数据库类型

修改 `OBSIDIAN_DB_DRIVER` 与 `OBSIDIAN_DB_DSN` 后，程序只会在目标数据库执行表结构迁移，不会自动导入原数据库的业务数据。

跨 SQLite 与 PostgreSQL 的迁移需要另行转换和核对数据。先验证账号、关联关系、附件与凭据解密，再切换正式流量。项目没有提供一键跨引擎迁移工具。