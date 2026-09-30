# 备份与迁移

完整实例备份需要数据库和实例密钥。个人导出只包含自己的对话与偏好，不能替代实例备份。

## 自动实例备份到 S3 或 Cloudflare R2

超级管理员可在“管理后台 → 实例备份”配置完整实例备份。默认每 24 小时运行一次，并保留最近 7 天的备份；可将间隔设为 1–168 小时，保留期设为 1–3650 天。也可以立即运行备份。启用前先用“测试存储连接”确认存储桶权限。

配置项包括 S3 兼容端点、存储桶、区域、对象前缀、Access Key ID 和 Secret Access Key。密钥在数据库中加密保存，界面只显示是否已配置，不会再次返回密钥。更换密钥时填写新值；留空会保留现有值。备份计划和最近运行状态也可通过控制台的 `backup status`、`backup configure`、`backup test` 和 `backup run` 命令查看或修改。凭据只能在超级管理员备份页面输入，避免进入命令历史。

Cloudflare R2 配置示例：

| 设置 | 示例 |
| --- | --- |
| Endpoint | `https://<ACCOUNT_ID>.r2.cloudflarestorage.com` |
| Region | `auto` |
| Bucket | 目标 R2 存储桶名称 |
| Prefix | 例如 `obsidian-arc/production` |

参见 [Cloudflare R2 S3 API](https://developers.cloudflare.com/r2/api/s3/api/)。访问密钥至少需要目标存储桶和备份前缀下的 `PutObject`、`ListBucket`、`DeleteObject` 权限。连接测试会在本站专属前缀下上传临时对象、列出并删除它。保留清理只会删除本站生成且超过保留期的备份对象，不会清理其他实例或手动命名的对象。

每份 `.arcbackup` 包含一致性数据库快照和数据库中保存的附件二进制，不包含 `secret.key`、`OBSIDIAN_SECRET_KEY`、存储密钥或自动备份配置。归档本身不是加密格式，其中会有对话内容等明文业务数据；请使用私有存储桶，并限制读取权限。必须在其他安全位置保留原实例密钥，否则无法解密恢复后的凭据等加密字段。

单个压缩归档最大 4 GiB，解压数据最大 8 GiB；SQLite 还要求数据库快照不超过 4 GiB。SQLite 快照和归档暂存文件可能同时存在，请为备份任务预留最多约 8 GiB 临时磁盘空间。PostgreSQL 快照使用只读、可重复读事务，不在网络上传输期间持有数据库事务。

## 从实例归档恢复

恢复是离线操作：先停止所有连接该数据库的 Arc 实例和写入，再对一个空目标数据库运行对应版本的二进制。目标必须使用与归档相同的数据库引擎和迁移版本；此工具不支持 SQLite 与 PostgreSQL 之间转换。恢复不会导入自动备份配置或存储凭据，恢复后需要由超级管理员重新配置并启用备份。

恢复前提供原来的 `OBSIDIAN_SECRET_KEY`，或将原 `secret.key` 放在 `OBSIDIAN_DATA_DIR` 指定的数据目录中。命令会先验证归档中的密钥校验信息；缺少原密钥或密钥不匹配时，会在创建或迁移目标数据库前退出。示例使用构建产物 `bin/obsidian-arc`：

```bash
OBSIDIAN_SECRET_KEY='<original-key>' \
OBSIDIAN_DB_DRIVER=sqlite \
OBSIDIAN_DB_DSN='file:/restore/obsidian.db' \
./bin/obsidian-arc restore-backup --file ./instance.arcbackup
```

PostgreSQL 目标也可以使用 `OBSIDIAN_DB_DRIVER=postgres` 和 `OBSIDIAN_DB_DSN` 指定一个空数据库。命令先运行迁移，再检查引擎、迁移版本、表结构和目标为空，最后在一个事务中导入数据；数据导入事务中的错误会回滚导入。此前已运行的数据库迁移不属于该事务。

迁移只建出归档那个实例当时有的表：以[插件包](../architecture/plugin-packages)装的插件，它的包本身就在归档里（`plugin_packages` 表），它的表和列由包里的迁移建出，不需要另外准备。有一种情况归档里没有包：插件被删除时选择了保留数据，表还在、包已经没了。这时命令会在写入任何数据之前停下，并写出缺哪一条迁移；把那个插件的 `.arcx` 放进 `OBSIDIAN_PLUGIN_DIR`（镜像里默认是 `/usr/local/share/obsidian-arc/plugins`，随部署自带的包就在那里）再运行即可。成功后将服务指向恢复好的目标并启动，检查登录、历史对话、附件和模型调用。不要在仍运行的实例上恢复，也不要将归档解压到数据库文件目录。

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
