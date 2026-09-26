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

## 终端与 SSH

网页终端始终在账户菜单里（用户组可以关掉），不需要任何环境变量。下面这组只控制它的第二个入口：不带浏览器的 SSH 访问。

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_SSH_ADDR` | 空 | 控制台 SSH 监听地址，例如 `:2222`；留空则完全不监听 |
| `OBSIDIAN_SSH_HOST_KEY` | 数据目录下的 `ssh_host_ed25519_key` | 主机密钥路径，首次启动时生成，权限 0600 |
| `OBSIDIAN_SSH_IDLE` | `30m` | 会话在提示符前闲置多久后断开 |
| `OBSIDIAN_SSH_MAX_SESSIONS` | `16` | 全局并发会话上限 |

默认不监听是有意的：这个端口接受密码认证，它应当因为运维人员主动开启而出现，而不是因为升级了一次程序而出现。

连上去的是控制台，不是服务器 shell——没有命令执行、没有文件传输、没有端口转发。认证与网页登录共用同一套密码校验和失败次数限制；能登录的账号和能打开网页终端的一样——管理员，以及用户组允许使用终端的普通账号。详见[终端](../admin/console)。

启动日志会打印主机指纹，首次连接时请核对。数据目录丢失会使指纹变化。

## 初始化管理员

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_ADMIN_USER` | 空 | 初始化管理员用户名 |
| `OBSIDIAN_ADMIN_PASSWORD` | 空 | 初始化管理员密码 |
| `OBSIDIAN_ADMIN_EMAIL` | 空 | 初始化管理员邮箱，可选 |

用户名与密码同时提供，且用户表为空时才会创建管理员。不提供时，首个注册用户成为管理员。它们不会覆盖已有账号。

## 邮件

新实例在管理员后台的「安全 → 邮件服务」填写 SMTP、发件地址和站点公开地址，保存后发送测试邮件，再开启注册邮箱验证。SMTP 密码在数据库中加密保存，后台只显示是否已设置，不会回传明文。站点公开地址同时用于验证链接、第三方登录回调和 OpenID issuer。

下面的环境变量保留给已有部署：数据库还没有后台邮件配置时，它们作为初始回退值；在后台保存邮件配置后，由后台配置整组接管。数据库连接、监听地址和实例密钥仍是启动前的部署配置。

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_SMTP_HOST` | 空 | SMTP 主机；未设置时邮件功能不可用 |
| `OBSIDIAN_SMTP_PORT` | `587` | SMTP 端口 |
| `OBSIDIAN_SMTP_USERNAME` | 空 | SMTP 用户名 |
| `OBSIDIAN_SMTP_PASSWORD` | 空 | SMTP 密码 |
| `OBSIDIAN_SMTP_FROM` | 空 | 发件人 |
| `OBSIDIAN_SMTP_TLS` | 端口为 465 时为 `true` | 是否从连接开始就使用 TLS；否则使用 STARTTLS |
| `OBSIDIAN_PUBLIC_URL` | 空 | 站点对外的基础地址。邮件里的链接、第三方登录的回调地址、以及 OpenID 发现文档里的 issuer 都用它 |

启用邮箱验证前，先在后台检查公开 HTTPS 地址并发出测试邮件。未验证账户可凭同一封邮件中的链接或六位验证码完成验证。

临时邮箱检测也在管理员后台配置。填入付费 UserCheck API 密钥并开启检测后，非免检域名的完整邮箱地址会发送到 [UserCheck 邮箱接口](https://www.usercheck.com/docs/api/email-endpoint)；密钥走 Bearer 请求头并在本地数据库中加密保存，不需要添加环境变量。后台可编辑免检域名，并选择接口故障时继续注册或暂停注册。测试地址会消耗一次付费查询。

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
