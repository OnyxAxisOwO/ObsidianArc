<p align="center">
  <img src="docs/public/logo.svg" width="64" height="64" alt="Obsidian Arc Logo">
</p>

<h1 align="center">Obsidian Arc</h1>

<p align="center">
  自托管多用户 AI 聊天服务与 API 网关。<br>
  A self-hosted multi-user AI chat service and API gateway.
</p>

<p align="center">
  <a href="#中文说明">中文说明</a> • <a href="#english">English</a> • <a href="https://obsidianarc.pages.dev">在线文档 / Documentation</a>
</p>

---

<a name="中文说明"></a>
## 中文说明

**Obsidian Arc** 是一个自托管的多用户 AI 对话服务与 API 网关。系统采用单一 Go 二进制文件内嵌前端单页应用，使用 SQLite 或 PostgreSQL 存储数据，不依赖 Redis、消息队列或外部缓存。

### 在线文档

完整的使用指南、管理手册、接口参考与架构设计已发布在 Cloudflare Pages：
👉 **[obsidianarc.pages.dev](https://obsidianarc.pages.dev)**

---

### 功能列表

- **用户管理与权限分组**：支持账号注册、登录与密码管理。管理员可通过用户组控制不同群体的可用模型列表与额度池。
- **邀请码**：注册可设为开放、仅限邀请或关闭三档；管理员可批量生成邀请码，或生成一个绑定分组与随机试用天数的自定义合作方码（用于合作/赞助商链接，默认已注册账户也可直接领取，无需重新注册）；账户也可开启个人邀请码，按「每邀请 N 人奖励 M 张重置卡」的节奏发放邀请奖励。
- **服务商聚合与模型路由**：兼容 Anthropic Messages API 及兼容 OpenAI 协议的接口（OpenAI、DeepSeek、xAI、OpenRouter、Groq、Ollama、vLLM）。支持单个提供商挂载多款模型、模型映射路由（外部模型 ID 路由到底层实际模型 ID）与模型别名（`api_name`）。
- **流式传输与推理过程解析**：基于 Server-Sent Events (SSE) 传输流式文本与推理思维链（reasoning effort / thinking budget）；客户端取消请求时即时终止向上游发送数据；支持在单条会话中途切换不同模型。
- **项目与多会话系统（Projects）**：支持创建独立项目（立项上下文），每个项目支持独立名称与系统指令（Prompt 继承）；侧边栏提供手风琴折叠栏与项目选项卡，支持在同一个项目下开启并切换多条对话分支；支持修改与删除项目（项目删除后会话自动脱离并保留）。
- **会话持久化与归档管理**：对话历史支持重命名、置顶、搜索与彻底删除；提供**会话归档**功能与全局开关（`chat.allow_archive`），支持 3 点下拉快捷菜单归档与专有归档面板（`/archive`）检索恢复；图片在浏览器本地压缩后上传；代码块支持命名、保存与复制。
- **开发者网关接口（`/v1`）**：支持 API Key 认证，提供以下端点：
  - `POST /v1/chat/completions`（OpenAI 对话兼容）
  - `POST /v1/messages` 与 `POST /v1/messages/count_tokens`（Anthropic Messages 兼容，支持 Claude Code）
  - `POST /v1/responses`（OpenAI Responses 兼容，支持 Codex 等开发工具）
  - `POST /v1/images/generations`（图像生成）
  - `GET /v1/models` 与 `GET /v1/models/{id}`（模型列表与详情）
  - 支持工具调用（Tool Calling / Function Calling）的统一协议转换。
- **管理工作台（Workbench）与终端**：管理后台采用模块化工作台架构，提供清晰的分组导航、响应式选项卡与草稿自动暂存（`settingsDraft`）。右上角账户菜单里的**终端**是一个命令行界面，所有账号都能用（用户组可以关掉），每个人只能运行自己在页面上本来就能做的事：普通用户管理自己的资料、密钥、对话、项目、额度、反馈与生图记录，管理员另外获得其授权对应的全部后台命令（`user`、`group`、`model`、`health`、`usage` 等）。覆盖全部后台管理接口和账户侧所有可设置、可更改的接口，支持多标签页、双语 `help`、Tab 补全与 `--json` 输出；设置 `OBSIDIAN_SSH_ADDR` 后可通过 `ssh 用户名@域名` 直接连接（连上的是终端，不是服务器 shell），并可作为单条命令被脚本调用。
- **服务健康与可用性监控**：实时记录运行时间与健康状态；对空闲模型进行定时探活；支持连续失败自动熔断与警告阈值（`health.warn_below`）；管理员可在系统设置中控制普通用户是否可见状态页面，对非管理员请求自动脱敏上游信息。
- **图像实验室（Image Lab）**：支持图像生成模型调用、宽高比切换、灯箱缩放与图片下载；支持在后台独立标记模型的生图能力。
- **用量核算与配额控制**：每次对话和 API 调用记录到用量账本；支持按 5 小时、周、月周期，针对请求次数、Token 消耗或积分设置限制；使用数据库行锁避免并发透支。
- **两步验证（2FA）**：用户可在「设置 → 安全」中按向导绑定身份验证器（TOTP，兼容 Google Authenticator、Microsoft Authenticator 等），扫码或手动输入密钥，并获得一次性恢复码；登录（含 GitHub / Google 登录与 SSH 终端）在密码之后再要求验证码。管理员可在「安全 → 两步验证」中设置强制策略：可选、管理员进入后台时必须绑定、所有管理员必须绑定、所有用户必须绑定；可设置验证器中显示的名称与「记住浏览器」天数，查看启用情况，并为丢失手机的用户重置两步验证。
- **界面与安全设计**：前端采用 Vue 模板插值渲染，不使用 `innerHTML` 与 `v-html`；上游 API Key 在数据库中加密存储；提供浅色、深色与跟随系统的界面配色。

---

### 运行与配置

#### 二进制直接运行

```bash
./obsidian-arc
```

默认在 `:8080` 端口启动，数据保存在 `./data` 目录（自动创建 SQLite 数据库）。首个注册的用户自动成为管理员。

在启动时通过环境变量指定管理员账号：

```bash
OBSIDIAN_ADMIN_USER=admin OBSIDIAN_ADMIN_PASSWORD='YourStrongPassword' ./obsidian-arc
```

#### 文档系统

文档页面已部署在 **[obsidianarc.pages.dev](https://obsidianarc.pages.dev)**，本地开发与编译指令如下：

```bash
# 启动本地文档开发预览
make docs-dev

# 编译文档静态站点
make docs
```

#### 常用环境变量

| 变量名 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSIDIAN_ADDR` | `:8080` | 服务监听地址与端口 |
| `OBSIDIAN_DB_DRIVER` | `sqlite` | 数据库类型：`sqlite` 或 `postgres` |
| `OBSIDIAN_DB_DSN` | `./data/obsidian.db` | 数据库连接字符串（PostgreSQL 必填） |
| `OBSIDIAN_SECRET_KEY` | 自动生成于 `./data` | 上游 API Key 加密密钥；多实例部署时需保持一致 |
| `OBSIDIAN_DATA_DIR` | `./data` | 数据库文件与密钥存储路径 |
| `OBSIDIAN_COOKIE_SECURE` | `true` | 是否仅允许 HTTPS 传输 Cookie；本地 HTTP 测试可设为 `false` |
| `OBSIDIAN_TRUST_PROXY` | `false` | 是否信任反向代理传递的 `X-Forwarded-For` 报头 |
| `OBSIDIAN_PUBLIC_URL` | 空 | 站点公开访问地址（用于邮箱验证链接） |
| `OBSIDIAN_SMTP_HOST` | 空 | SMTP 服务器地址（用于发送验证邮件） |
| `OBSIDIAN_SSH_ADDR` | 空 | 终端的 SSH 监听地址（例如 `:2222`） |

#### Docker 部署

```bash
# 使用 SQLite 运行
docker build -t obsidian-arc .
docker run -d -p 8080:8080 -v arc-data:/data \
  -e OBSIDIAN_SECRET_KEY=$(openssl rand -hex 32) \
  obsidian-arc
```

使用 PostgreSQL 运行（`docker-compose.yml`）：

```bash
OBSIDIAN_SECRET_KEY=$(openssl rand -hex 32) docker compose up -d
```

---

<a name="english"></a>
## English

**Obsidian Arc** is a self-hosted multi-user AI chat service and API gateway. The entire application runs as a single Go binary with an embedded Vue 3 SPA frontend and uses SQLite or PostgreSQL for persistence, requiring no Redis, message queues, or external caches.

### Documentation

Online documentation is hosted on Cloudflare Pages:
👉 **[obsidianarc.pages.dev](https://obsidianarc.pages.dev)**

---

### Capabilities

- **Accounts and Groups**: Registration, authentication, and session management. User groups allow operators to assign model access lists and shared credit allowances.
- **Invite Codes**: Registration can be open, invite-only, or closed. Administrators mint batches of codes, or one named partner code with a target group and a randomized trial length for sponsor and affiliate links — claimable by an existing account as well as a new signup by default. Accounts can also carry a personal invite code that pays out reset cards on a configurable "every N invites" cadence.
- **Provider Aggregation and Model Routing**: Compatible with Anthropic Messages API and OpenAI-compatible endpoints (OpenAI, DeepSeek, xAI, OpenRouter, Groq, Ollama, vLLM). Supports mapping multiple models per provider, model routing (mapping exposed names to internal identifiers), and aliases (`api_name`).
- **Streaming and Reasoning Display**: Server-Sent Events (SSE) streaming with live reasoning effort and thinking budget extraction. Client disconnection cancels outbound requests to stop provider billing. Supports switching models mid-conversation.
- **Projects & Multi-conversations**: Standalone project contexts with system prompt inheritance for all member sessions; sidebar accordion supporting multiple conversations per project; full project lifecycle management (creation, rename, instructions update, and deletion without transcript loss).
- **Conversations & Archiving**: Search, pin, rename, and delete conversation history; conversation archiving with global toggle (`chat.allow_archive`), 3-dots action menu, dedicated `/archive` panel for reviewing and unarchiving conversations; client-side image compression; code block actions.
- **Developer API Gateway (`/v1`)**: API Key authentication covering:
  - `POST /v1/chat/completions` (OpenAI format)
  - `POST /v1/messages` and `POST /v1/messages/count_tokens` (Anthropic Messages format, compatible with Claude Code)
  - `POST /v1/responses` (OpenAI Responses format, compatible with Codex)
  - `POST /v1/images/generations` (image generation)
  - `GET /v1/models` and `GET /v1/models/{id}` (model listings)
  - Unified tool calling and function calling format conversion.
- **Admin Workbench & Terminal**: Modular administration workbench with categorized navigation, responsive tabs, and automatic form draft recovery (`settingsDraft`). The **Terminal** in the account menu is a command line every account can open (a group can switch it off), and it runs only what that account could already do on its own screens: an ordinary account manages its own profile, keys, conversations, projects, credit, feedback and image history, and an administrator also gets the backoffice commands their grants cover (`user`, `group`, `model`, `health`, `usage`, …). It covers every administrative endpoint and every account-side setting that can be changed, with bilingual `help`, tab completion, and `--json` output. Setting `OBSIDIAN_SSH_ADDR` serves the same terminal over SSH (`ssh user@host`), scriptable one command at a time.
- **Uptime Monitoring and Circuit Breaking**: Uptime tracking and health status checks; automated periodic probe checks for idle models; automatic circuit breaking on consecutive upstream failures and warning thresholds (`health.warn_below`); configurable public visibility with credential stripping for non-admin requests.
- **Image Lab**: Standalone image generation interface with aspect-ratio selection, lightbox zoom, and download controls; capability flags distinguish drawing models from text models.
- **Usage Accounting and Rate Limits**: Per-turn usage recorded into an append-only ledger; supports request count, token, and credit limits across 5-hour, weekly, and monthly windows; row-level database locks prevent concurrent overdrafts.
- **Two-step verification (2FA)**: A guided setup under Settings → Security binds an authenticator app (TOTP — Google Authenticator, Microsoft Authenticator and the like) by QR code or typed key, with one-time recovery codes. Signing in — with a password, with GitHub or Google, or over the SSH terminal — then asks for a code as well. Operators choose who must have it (nobody, administrators before using the backoffice, all administrators, or everyone), the name apps show, and how long a browser may be remembered; they can see adoption and reset it for somebody who lost their phone.
- **Security and Rendering**: Frontend uses Vue template bindings with no `innerHTML` or `v-html`; upstream credentials stored encrypted; dark, light, and system theme options.

---

## 资源开销 / Resource Costs

测试环境：单进程、SQLite 驱动：

| 指标 / Metric | 实测数据 / Measurement |
| --- | --- |
| 二进制体积 / Binary size | 21.6 MB（Linux amd64；使用 `-tags nosqlite` 为 17.9 MB） |
| 冷启动就绪时间 / Cold start | ~28 ms |
| 空闲内存占用 / Idle RSS | ~16 MB |
| 20 并发流式峰值 / Peak under 20 concurrency | ~54 MB 内存，11 个 OS 线程 |
| 首次加载传输体积 / Wire payload | 打开对话界面传输 208.00 kB（170.35 kB JS + 37.65 kB CSS）；中文语言包 (37.71 kB)、管理后台 (87.13 kB)、终端 (7.24 kB)、公式渲染器 (3.63 kB)、访客官网首页 (3.98 kB) 按需分包加载 |
| 后台常驻协程 / Background goroutines | 1 个（10 分钟周期的系统清理协程） |
| Go 直接依赖 / Direct Go dependencies | 3 个（SQLite 驱动、pgx、x/crypto） |
| 前端运行时依赖 / Frontend runtime dependencies | 4 个（`vue`、`vue-router`、`@vueuse/core`、`lucide-vue-next`） |

---

## 贡献者 / Contributors

### 维护者 / Maintainers
- **[OnyxAxisOwO](https://github.com/OnyxAxisOwO/)** — Creator & Lead Maintainer
- **[Abloom](https://github.com/abloom25)** — Frontend Engineering & Performance Optimization
- **[amnssb](https://github.com/amnssb)** — Feature Contributions

### 协作开发模型 / Co-developed with AI Models
- **Claude Opus 5**
- **GPT 6 Sol**
- **GPT 5.6 Sol**
- **Gemini 3.8 Flash**

---

## 开源协议 / License

[MIT License](LICENSE)
