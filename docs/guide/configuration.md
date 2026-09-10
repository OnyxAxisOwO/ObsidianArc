# 配置参考

启动配置通过 `OBSIDIAN_` 环境变量读取，修改后需要重启程序。站点名称、注册规则、模型和额度等业务设置由后台保存到数据库，见[系统设置](../admin/system-settings)。

程序不读取专用配置文件。下面的默认值以 `internal/config/config.go` 为准；空值通常按未设置处理。

## 基础配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_ADDR` | `:8080` | HTTP 监听地址 |
| `OBSIDIAN_DATA_DIR` | `./data` | 数据目录，相对于进程工作目录 |
| `OBSIDIAN_LOG_LEVEL` | `info` | 日志级别 |
| `OBSIDIAN_DEV` | `false` | 开发模式，将前端请求代理到本机 Vite |
| `OBSIDIAN_SECRET_KEY` | 自动生成 | 至少 16 个字符；未设置时读取或生成数据目录中的 `secret.key` |

实例密钥用于派生服务商凭据的加密密钥。需要固定保存，不要在每次启动时重新生成。

## 数据库

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_DB_DRIVER` | `sqlite` | SQLite 或 PostgreSQL |
| `OBSIDIAN_DB_DSN` | 数据目录下的 `obsidian.db` | PostgreSQL 模式必填 |
| `OBSIDIAN_DB_MAX_CONNS` | SQLite 为 `4`，PostgreSQL 为 `10` | 最大打开连接数 |
| `OBSIDIAN_DB_MAX_IDLE_CONNS` | `2` | 最大空闲连接数，不高于打开连接上限 |
| `OBSIDIAN_DB_CONN_MAX_IDLE` | `5m` | 空闲连接的最长保留时间 |

驱动别名支持 `sqlite3`、`postgresql` 和 `pgx`，分别归一为 `sqlite` 或 `postgres`。

切换数据库不会自动迁移业务数据。多个实例连接同一数据库时必须使用相同的实例密钥；设置缓存和部分内存限制仍有实例边界，见[架构设计](../architecture/design-philosophy)。

## 登录会话与代理

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_SESSION_TTL` | `720h` | 登录会话有效期 |
| `OBSIDIAN_SESSION_COOKIE` | `obsidian_session` | Cookie 名称 |
| `OBSIDIAN_COOKIE_SECURE` | 非开发模式为 `true` | Cookie 是否仅通过安全连接发送 |
| `OBSIDIAN_SESSION_TOUCH_INTERVAL` | `1h` | 活跃时间写回间隔 |
| `OBSIDIAN_TRUST_PROXY` | `false` | 是否信任配置来源的代理转发头 |
| `OBSIDIAN_TRUSTED_PROXIES` | 空 | 代理地址或 CIDR，逗号分隔 |
| `OBSIDIAN_ALLOWED_ORIGINS` | 空 | 同源写入检查额外允许的 Origin，逗号分隔 |

本机 HTTP 测试可关闭 Secure Cookie。对外使用时应配置 HTTPS。

开启代理信任但未指定来源时，会回退到私有网段规则。实际部署应明确填写代理来源，并限制程序端口的直接访问。

`OBSIDIAN_ALLOWED_ORIGINS` 是写入请求来源检查的允许列表，不代表完整的跨域 CORS 配置。

## 初始化管理员

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_ADMIN_USER` | 空 | 初始化管理员用户名 |
| `OBSIDIAN_ADMIN_PASSWORD` | 空 | 初始化管理员密码 |
| `OBSIDIAN_ADMIN_EMAIL` | 空 | 初始化管理员邮箱，可选 |

用户名与密码同时提供，且用户表为空时才会创建管理员。不提供时，首个注册用户成为管理员。它们不会覆盖已有账号。

## 邮件

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_SMTP_HOST` | 空 | SMTP 主机；未设置时邮件功能不可用 |
| `OBSIDIAN_SMTP_PORT` | `587` | SMTP 端口 |
| `OBSIDIAN_SMTP_USERNAME` | 空 | SMTP 用户名 |
| `OBSIDIAN_SMTP_PASSWORD` | 空 | SMTP 密码 |
| `OBSIDIAN_SMTP_FROM` | 空 | 发件人 |
| `OBSIDIAN_SMTP_TLS` | 端口为 465 时为 `true` | 是否从连接开始就使用 TLS；否则使用 STARTTLS |
| `OBSIDIAN_PUBLIC_URL` | 空 | 邮件链接使用的站点基础地址 |

SMTP 凭据通过环境变量配置，不在系统设置表单中填写。启用邮箱验证前先验证邮件发送与链接地址。

## 上游连接

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_UPSTREAM_DIAL_TIMEOUT` | `10s` | 建立连接超时 |
| `OBSIDIAN_UPSTREAM_HEADER_TIMEOUT` | `90s` | 等待 HTTP 响应头超时 |
| `OBSIDIAN_UPSTREAM_REQUEST_TIMEOUT` | `0` | 整次上游请求时限；0 表示不额外设置总时限 |
| `OBSIDIAN_UPSTREAM_MAX_IDLE_CONNS` | `32` | 最大空闲连接数 |
| `OBSIDIAN_UPSTREAM_IDLE_TIMEOUT` | `90s` | 空闲连接保留时间 |

响应头超时不是首个文本 Token 的超时。长推理请求可能很早发出响应头，却过一段时间才输出正文。

## 密码哈希

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_ARGON_MEMORY_KIB` | `19456` | 每次哈希使用的内存，KiB |
| `OBSIDIAN_ARGON_ITERATIONS` | `2` | 迭代次数 |
| `OBSIDIAN_ARGON_PARALLELISM` | `1` | 单次计算并行度 |
| `OBSIDIAN_ARGON_MAX_PARALLEL` | CPU 核数与 4 中较小者 | 同时执行的哈希数量，最低为 1 |

时间参数使用 Go duration 格式，如 `10s`、`5m`、`720h`，不要写成 `30d`。布尔值建议使用 `true` 或 `false`。