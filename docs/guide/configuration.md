# 配置参考

```text
所有配置均为环境变量，前缀是 OBSIDIAN_，代码中未读取配置文件。表格中的“配置文件字段”列均标记 TODO。
```

| 配置项（环境变量） | 配置文件字段 | 类型 | 默认值 | 必填 | 说明 |
| --- | --- | --- | --- | --- | --- |
| `OBSIDIAN_ADDR` | `<!-- TODO: 未在代码中确认 -->` | string | `:8080` | 否 | 监听地址，示例 `127.0.0.1:8080`。 |
| `OBSIDIAN_DATA_DIR` | `<!-- TODO: 未在代码中确认 -->` | string | `./data` | 否 | 数据目录，启动时转为绝对路径并创建。 |
| `OBSIDIAN_DEV` | `<!-- TODO: 未在代码中确认 -->` | bool | `false` | 否 | 开发模式开关。 |
| `OBSIDIAN_DB_DRIVER` | `<!-- TODO: 未在代码中确认 -->` | string | `sqlite` | 否 | 支持 `sqlite`、`postgres`、`postgresql`、`pgx`，其余值拒绝。 |
| `OBSIDIAN_DB_DSN` | `<!-- TODO: 未在代码中确认 -->` | string | sqlite: `dataDir/obsidian.db` | Postgres 场景时是 | PostgreSQL 场景必须显式设置。 |
| `OBSIDIAN_DB_MAX_CONNS` | `<!-- TODO: 未在代码中确认 -->` | int | sqlite: `4`，postgres: `10` | 否 | 数据库最大打开连接数。 |
| `OBSIDIAN_DB_MAX_IDLE_CONNS` | `<!-- TODO: 未在代码中确认 -->` | int | `2` | 否 | 数据库最大空闲连接数。 |
| `OBSIDIAN_DB_CONN_MAX_IDLE` | `<!-- TODO: 未在代码中确认 -->` | duration | `5m` | 否 | 数据库连接最大空闲时长。 |
| `OBSIDIAN_LOG_LEVEL` | `<!-- TODO: 未在代码中确认 -->` | string | `info` | 否 | 日志级别。 |
| `OBSIDIAN_TRUST_PROXY` | `<!-- TODO: 未在代码中确认 -->` | bool | `false` | 否 | 开启后读取反向代理头提取客户端 IP。 |
| `OBSIDIAN_TRUSTED_PROXIES` | `<!-- TODO: 未在代码中确认 -->` | string（逗号分隔） | 空 | 否 | 可用列表约束代理来源。 |
| `OBSIDIAN_ALLOWED_ORIGINS` | `<!-- TODO: 未在代码中确认 -->` | string（逗号分隔） | 空 | 否 | 额外 CORS 白名单。 |
| `OBSIDIAN_SECRET_KEY` | `<!-- TODO: 未在代码中确认 -->` | string | 自动生成并写入 `secret.key` | 否 | 长度低于 16 会启动失败。 |
| `OBSIDIAN_SESSION_TTL` | `<!-- TODO: 未在代码中确认 -->` | duration | `720h` | 否 | 登录会话生命周期。 |
| `OBSIDIAN_SESSION_COOKIE` | `<!-- TODO: 未在代码中确认 -->` | string | `obsidian_session` | 否 | 会话 Cookie 名称。 |
| `OBSIDIAN_COOKIE_SECURE` | `<!-- TODO: 未在代码中确认 -->` | bool | `!OBSIDIAN_DEV` | 否 | 默认在非开发模式为 true。 |
| `OBSIDIAN_SESSION_TOUCH_INTERVAL` | `<!-- TODO: 未在代码中确认 -->` | duration | `1h` | 否 | 会话活跃刷新间隔。 |
| `OBSIDIAN_SMTP_HOST` | `<!-- TODO: 未在代码中确认 -->` | string | 空 | 否 | SMTP 主机。 |
| `OBSIDIAN_SMTP_PORT` | `<!-- TODO: 未在代码中确认 -->` | int | `587` | 否 | SMTP 端口。 |
| `OBSIDIAN_SMTP_USERNAME` | `<!-- TODO: 未在代码中确认 -->` | string | 空 | 否 | SMTP 用户名。 |
| `OBSIDIAN_SMTP_PASSWORD` | `<!-- TODO: 未在代码中确认 -->` | string | 空 | 否 | SMTP 密码。 |
| `OBSIDIAN_SMTP_FROM` | `<!-- TODO: 未在代码中确认 -->` | string | 空 | 否 | 发件人地址。 |
| `OBSIDIAN_PUBLIC_URL` | `<!-- TODO: 未在代码中确认 -->` | string | 空 | 否 | 站点外链前缀。 |
| `OBSIDIAN_SMTP_TLS` | `<!-- TODO: 未在代码中确认 -->` | bool | `port == 465` 自动判断 | 否 | 465 端口默认开启 TLS。 |
| `OBSIDIAN_ARGON_MEMORY_KIB` | `<!-- TODO: 未在代码中确认 -->` | int | `19456` | 否 | Argon2 内存。 |
| `OBSIDIAN_ARGON_ITERATIONS` | `<!-- TODO: 未在代码中确认 -->` | int | `2` | 否 | Argon2 迭代次数。 |
| `OBSIDIAN_ARGON_PARALLELISM` | `<!-- TODO: 未在代码中确认 -->` | int | `1` | 否 | Argon2 并行度。 |
| `OBSIDIAN_ARGON_MAX_PARALLEL` | `<!-- TODO: 未在代码中确认 -->` | int | `min(4, CPU核数)` | 否 | Argon2 同时执行上限。 |
| `OBSIDIAN_UPSTREAM_DIAL_TIMEOUT` | `<!-- TODO: 未在代码中确认 -->` | duration | `10s` | 否 | 上游拨号超时。 |
| `OBSIDIAN_UPSTREAM_HEADER_TIMEOUT` | `<!-- TODO: 未在代码中确认 -->` | duration | `90s` | 否 | 上游首字响应超时。 |
| `OBSIDIAN_UPSTREAM_REQUEST_TIMEOUT` | `<!-- TODO: 未在代码中确认 -->` | duration | `0` | 否 | 单次上游请求超时。 |
| `OBSIDIAN_UPSTREAM_MAX_IDLE_CONNS` | `<!-- TODO: 未在代码中确认 -->` | int | `32` | 否 | 上游长连接池上限。 |
| `OBSIDIAN_UPSTREAM_IDLE_TIMEOUT` | `<!-- TODO: 未在代码中确认 -->` | duration | `90s` | 否 | 上游长连接空闲回收。 |
| `OBSIDIAN_ADMIN_USER` | `<!-- TODO: 未在代码中确认 -->` | string | 空 | 否 | 与 ADMIN_PASSWORD 同时存在时在初始化时创建管理员。 |
| `OBSIDIAN_ADMIN_PASSWORD` | `<!-- TODO: 未在代码中确认 -->` | string | 空 | 否 | 与 ADMIN_USER 同时存在时在初始化时创建管理员。 |
| `OBSIDIAN_ADMIN_EMAIL` | `<!-- TODO: 未在代码中确认 -->` | string | 空 | 否 | 与 ADMIN_USER 同时存在时在初始化时创建管理员。 |

## 配置文件字段未确认的标注处理规则

```text
表内“配置文件字段”列全部使用
<!-- TODO: 未在代码中确认 -->
```
