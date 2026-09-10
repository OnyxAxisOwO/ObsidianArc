<p align="center">
  <img src="docs/public/logo.svg" width="64" height="64" alt="Obsidian Arc Logo">
</p>

<h1 align="center">Obsidian Arc</h1>

<p align="center">
  自托管多用户 AI 聊天服务与 API 网关。<br>
  A self-hosted multi-user AI chat service and API gateway.
</p>

<p align="center">
  <a href="#中文说明">中文说明</a> • <a href="#english">English</a> • <a href="docs/">文档系统 / Documentation</a>
</p>

---

<a name="中文说明"></a>
## 中文说明

**Obsidian Arc** 是一个自托管的多用户 AI 对话服务与 API 网关。系统采用单一 Go 二进制文件内嵌前端单页应用，使用 SQLite 或 PostgreSQL 存储数据，不依赖 Redis、消息队列或外部缓存。

### 功能列表

- **用户管理与权限分组**：支持账号注册、登录与密码管理。管理员可通过用户组控制不同群体的可用模型列表与额度池。
- **服务商聚合与模型路由**：兼容 Anthropic Messages API 及兼容 OpenAI 协议的接口（OpenAI、DeepSeek、xAI、OpenRouter、Groq、Ollama、vLLM）。支持单个提供商挂载多款模型、模型映射路由（外部模型 ID 路由到底层实际模型 ID）与模型别名（`api_name`）。
- **流式传输与推理过程解析**：基于 Server-Sent Events (SSE) 传输流式文本与推理思维链（reasoning effort / thinking budget）；客户端取消请求时即时终止向上游发送数据；支持在单条会话中途切换不同模型。
- **开发者网关接口（`/v1`）**：支持 API Key 认证，提供以下端点：
  - `POST /v1/chat/completions`（OpenAI 对话兼容）
  - `POST /v1/messages` 与 `POST /v1/messages/count_tokens`（Anthropic Messages 兼容，支持 Claude Code）
  - `POST /v1/responses`（OpenAI Responses 兼容，支持 Codex 等开发工具）
  - `POST /v1/images/generations`（图像生成）
  - `GET /v1/models` 与 `GET /v1/models/{id}`（模型列表与详情）
  - 支持工具调用（Tool Calling / Function Calling）的统一协议转换。
- **服务健康与可用性监控**：记录系统运行时间与健康状态；对空闲模型进行定时探活；支持连续失败自动熔断；管理员可在系统设置中控制普通用户是否可见状态页面，对非管理员请求自动脱敏上游信息。
- **图像实验室（Image Lab）**：支持图像生成模型调用、宽高比切换、灯箱缩放与图片下载；支持在后台独立标记模型的生图能力。
- **用量核算与配额控制**：每次对话和 API 调用记录到用量账本；支持按 5 小时、周、月周期，针对请求次数、Token 消耗或积分设置限制；使用数据库行锁避免并发透支。
- **会话持久化与文件处理**：对话历史支持重命名、搜索与删除；图片在浏览器本地压缩后上传；代码块支持命名、保存与复制。
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

完整文档采用 VitePress 构建，存放在 `docs/` 目录下：

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

### Capabilities

- **Accounts and Groups**: Registration, authentication, and session management. User groups allow operators to assign model access lists and shared credit allowances.
- **Provider Aggregation and Model Routing**: Compatible with Anthropic Messages API and OpenAI-compatible endpoints (OpenAI, DeepSeek, xAI, OpenRouter, Groq, Ollama, vLLM). Supports mapping multiple models per provider, model routing (mapping exposed names to internal identifiers), and aliases (`api_name`).
- **Streaming and Reasoning Display**: Server-Sent Events (SSE) streaming with live reasoning effort and thinking budget extraction. Client disconnection cancels outbound requests to stop provider billing. Supports switching models mid-conversation.
- **Developer API Gateway (`/v1`)**: API Key authentication covering:
  - `POST /v1/chat/completions` (OpenAI format)
  - `POST /v1/messages` and `POST /v1/messages/count_tokens` (Anthropic Messages format, compatible with Claude Code)
  - `POST /v1/responses` (OpenAI Responses format, compatible with Codex)
  - `POST /v1/images/generations` (image generation)
  - `GET /v1/models` and `GET /v1/models/{id}` (model listings)
  - Unified tool calling and function calling format conversion.
- **Uptime Monitoring and Circuit Breaking**: Uptime tracking and health status checks; automated periodic probe checks for idle models; automatic circuit breaking on consecutive upstream failures; configurable public visibility with credential stripping for non-admin requests.
- **Image Lab**: Standalone image generation interface with aspect-ratio selection, lightbox zoom, and download controls; capability flags distinguish drawing models from text models.
- **Usage Accounting and Rate Limits**: Per-turn usage recorded into an append-only ledger; supports request count, token, and credit limits across 5-hour, weekly, and monthly windows; row-level database locks prevent concurrent overdrafts.
- **Conversations and Attachments**: Conversation history search, renaming, and deletion; client-side image compression before upload; code blocks with naming, copying, and saving.
- **Security and Rendering**: Frontend uses Vue template bindings with no `innerHTML` or `v-html`; upstream credentials stored encrypted; dark, light, and system theme options.

---

## 资源开销 / Resource Costs

测试环境：单进程、SQLite 驱动：

| 指标 / Metric | 实测数据 / Measurement |
| --- | --- |
| 二进制体积 / Binary size | 18.2 MB（Linux amd64；使用 `-tags nosqlite` 为 14.5 MB） |
| 冷启动就绪时间 / Cold start | ~28 ms |
| 空闲内存占用 / Idle RSS | ~16 MB |
| 20 并发流式峰值 / Peak under 20 concurrency | ~54 MB 内存，11 个 OS 线程 |
| 首次加载传输体积 / Wire payload | 打开对话界面传输 128.3 kB（111.6 kB JS + 16.6 kB CSS）；中文语言包 (18.3 kB)、管理后台 (35.8 kB)、公式渲染器 (3.7 kB) 按需分包加载 |
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
- **GPT 5.6 Sol**
- **Gemini 3.8 Flash**

---

## 开源协议 / License

[MIT License](LICENSE)
