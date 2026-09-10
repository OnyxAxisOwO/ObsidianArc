# Obsidian Arc

<p align="center">
  <strong>专为 <a href="https://ai.onyxaxis.org">Axis AI</a> 打造的高可用、自托管多用户 AI 聊天与 API 网关中枢。</strong><br>
  <strong>A self-hosted, high-availability multi-user AI chat & gateway hub built for <a href="https://ai.onyxaxis.org">Axis AI</a>.</strong>
</p>

<p align="center">
  <a href="#中文说明">中文说明</a> • <a href="#english">English</a>
</p>

---

<a name="中文说明"></a>
## 中文说明

**Obsidian Arc** 是一个专为 [Axis AI](https://ai.onyxaxis.org) 量身定制的高性能自托管 AI 对话与网关中枢：支持多用户体系、多模型与服务商路由聚合、开箱即用的管理员后台、持久化对话流以及基于用量账本的高精度配额核算系统。

整个系统仅需**一个内嵌了前端的 Go 二进制文件**和**一个数据库**（支持 SQLite 或 PostgreSQL），无需依赖 Redis、消息队列、微服务 Sidecar 或外部缓存层。

项目源自网页主题扩展 [PageDye](https://github.com/OnyxAxisOwO/PageDye) 的内置 AI 模块，后演进为极简的高颜值独立对话界面，并最终构建为现在的生产级服务中枢。

### 核心特性

- **多账户与权限用户组**：完整的注册、登录、登出流程；通过灵活的用户组定义各群体可使用的模型列表与共用额度池。
- **全服务商兼容**：原生支持 Anthropic Messages API 及任意兼容 OpenAI 规范的端点（OpenAI、DeepSeek、xAI、OpenRouter、Groq，以及本地部署的 Ollama / vLLM）。无需重启，在管理后台即可动态增删上游提供商与模型。
- **服务商与模型解耦 & 路由调度**：单个服务商可挂载多款模型；支持模型透明路由（对外展示 A 模型，底层由 B 模型响应，仅管理员可见路由规则）；支持重复模型 ID 自动防冲突命名与自定义 `api_name` 别名。
- **实时流式与统一推理思维链**：基于 Server-Sent Events (SSE) 实现打字机流式输出；原生解析模型思考过程（Reasoning Effort / Thinking Budget），前端一键调节三档推理深度；点击“停止”即时向中断上游请求，中断计费。
- **完整的 Agent API 兼容体系**：在 `/v1` 下提供符合行业标准的无状态 Key 鉴权端点：
  - OpenAI `chat/completions`
  - Anthropic `messages`（完美兼容 Claude Code）
  - OpenAI `responses`（支持 Codex 等现代化开发助手）
  - 模型工具调用（Tool Calling / Function Calling）全渠道穿透与统一转换。
- **实时 Uptime 可用性监控**：
  - 统计系统启动时长、实时健康状态（全部正常 / 部分降级 / 服务故障）以及各模型的在线率与状态指示灯；
  - 区分真实用户流量与后台自动轻量探测（Probe）；
  - 支持连续失败自动熔断；管理员可在系统设置中一键控制普通用户是否可见该侧边栏页面，非管理员请求自动脱敏并屏蔽上游私密信息。
- **画图实验室（Image Lab）**：支持多款生图模型、风格选择（配有不占用水平空间的纤细平滑悬浮滚动条）、宽高比切换、局部光箱缩放与一键下载。
- **模型详情与精准日志排查**：后台模型列表点击即展示全局唯一 ID（支持一键复制），并提供“查看历史报错信息”专属快捷入口，直达当前模型的失败请求排查视图。
- **会话持久化与分支编辑**：支持对话历史检索、重命名、导出与软硬删除；支持回退到历史提问进行重新编辑并重新分支生成后续回答。
- **图片与文件上下文**：图片在浏览器本地自适应压缩后再安全上传；支持文本与代码文件一键折叠并入上下文。
- **精准配额与用量账本**：每次对话均写入审计记录；支持按 5 小时、周、月维度，针对请求次数、Token 消耗或点数余额进行“全局 → 用户组 → 个人”三级约束。
- **主题与色调系统**：原生支持浅色 / 深色 / 跟随系统，自适应对比度色彩调节算法。
- **全站公告与通知系统**：支持针对特定用户或全站推送弹出通知或未读小红点。
- **纯粹且安全**：前端全面杜绝 `innerHTML` 和 `v-html`，安全防范 XSS 漏洞；后端坚持原子化数据库行锁，无并发超售风险。

---

### 运行与配置

#### 快速启动

```bash
./obsidian-arc
```

无需任何额外参数即可在 `:8080` 端口直接启动。系统会自动创建 `./data` 目录、初始化 SQLite 数据库并应用迁移。注册的第一个账户自动晋升为管理员。

如果希望以无头方式在首次启动时直接创建管理员账号：

```bash
OBSIDIAN_ADMIN_USER=admin OBSIDIAN_ADMIN_PASSWORD='YourStrongPassword' ./obsidian-arc
```

#### 常用环境变量

| 变量名 | 默认值 | 作用说明 |
| --- | --- | --- |
| `OBSIDIAN_ADDR` | `:8080` | 监听地址与端口 |
| `OBSIDIAN_DB_DRIVER` | `sqlite` | 数据库驱动：`sqlite` 或 `postgres` |
| `OBSIDIAN_DB_DSN` | `./data/obsidian.db` | 数据库连接字符串（PostgreSQL 必填） |
| `OBSIDIAN_SECRET_KEY` | 自动生成保存至 `./data` | 用于加密上游 API Key 的密钥。多实例集群部署时需保持一致 |
| `OBSIDIAN_DATA_DIR` | `./data` | 数据库与密钥存放目录 |
| `OBSIDIAN_COOKIE_SECURE` | `true` | 是否启用安全 Cookie（本地测试 HTTP 时可置为 `false`） |
| `OBSIDIAN_TRUST_PROXY` | `false` | 是否信任反向代理传递的客户端真实 IP |
| `OBSIDIAN_PUBLIC_URL` | — | 站点的公开访问基准 URL（用于邮件验证等） |
| `OBSIDIAN_SMTP_HOST` | — | SMTP 邮件服务器地址（开启邮箱验证时需配置） |

#### Docker 部署

使用 Docker 运行：

```bash
docker build -t obsidian-arc .
docker run -d -p 8080:8080 -v arc-data:/data \
  -e OBSIDIAN_SECRET_KEY=$(openssl rand -hex 32) \
  obsidian-arc
```

搭配 PostgreSQL 运行（`docker-compose.yml`）：

```bash
OBSIDIAN_SECRET_KEY=$(openssl rand -hex 32) docker compose up -d
```

---

<a name="english"></a>
## English

**Obsidian Arc** is a high-performance, self-hosted AI chat server and unified gateway hub built specifically for [Axis AI](https://ai.onyxaxis.org). It offers multi-user management, runtime provider routing, an admin backoffice, conversation history, and fine-grained ledger-based usage accounting.

One single Go binary with the embedded Vue 3 SPA frontend, one database (SQLite or PostgreSQL), and zero extra moving parts — no Redis, no sidecars, no message brokers.

### Key Capabilities

- **Accounts & Groups**: Role-based access control with groups defining accessible models, rate limits, and credit pools.
- **Universal Provider Support**: Supports Anthropic's Messages API and any OpenAI-compatible API (OpenAI, DeepSeek, xAI, OpenRouter, Groq, local Ollama or vLLM). Managed dynamically from the admin panel at runtime.
- **Provider & Model Decoupling**: Map multiple models to upstream providers with transparent model routing, automated anti-collision name qualification, and custom `api_name` aliases.
- **Streaming & Live Reasoning**: Server-Sent Events (SSE) with live thinking/reasoning effort display and cancellation-aware generation.
- **Agent API Ecosystem**: Key-authenticated endpoints under `/v1` supporting OpenAI `chat/completions`, Anthropic `messages` (e.g. Claude Code), and OpenAI `responses` (Codex), with full tool-calling parity.
- **Real-Time Uptime Monitoring**: System running duration, health state detection, automated probe checks for idle models, and admin-toggleable user visibility with upstream credential stripping.
- **Image Lab**: Prompt-based image generation, aspect-ratio selection, lightbox zoom, and floating non-intrusive scrollbars.
- **Model Unique ID & Error History**: Inspect unique model ULIDs with 1-click copy, and drill straight into filtered error logs from the model editor.
- **Persistent Conversations**: Rich markdown rendering without `innerHTML`, local image downscaling, code folding, and turn regenerations.
- **Atomic Quotas**: Atomic balance upserts preventing race conditions and concurrent overdrafts without needing a separate Redis instance.

---

## 性能与运行开销 / Resource Costs

测试基准环境：单进程、SQLite 驱动（测试数据已校准）：

| 评估项 / Metric | 实测数据 / Measurement |
| --- | --- |
| 二进制文件大小 / Binary size | 18.2 MB（Linux amd64，使用 `-tags nosqlite` 纯 Postgres 构建为 14.5 MB） |
| 冷启动就绪时间 / Cold start | ~28 ms |
| 常驻空闲内存 / Idle RSS | ~16 MB |
| 20 并发流式交互峰值 / Peak under 20 concurrency | ~54 MB 内存，11 OS 线程 |
| 前端网络传输开销 / Wire payload | 进入聊天仅需传输 128.3 kB（111.6 kB JS + 16.6 kB CSS）；中文语言包 (18.3 kB)、管理后台 (35.8 kB)、数学公式渲染器 (3.7 kB) 独立按需加载 |
| 空闲后台协程 / Background goroutines | 1（仅运行 10 分钟周期的系统清理协程） |
| 直接 Go 依赖 / Direct Go dependencies | 3 个（纯 Go SQLite 驱动、pgx、x/crypto） |
| 前端运行时依赖 / Frontend runtime dependencies | 4 个（Vue、Vue Router、VueUse、Lucide Icons） |

---

## 贡献者与特别致谢 / Contributors & Acknowledgements

本项目专为 **[Axis AI](https://ai.onyxaxis.org)** 倾力打造，凝聚了开发者社区与前沿大模型的共同智慧：

### 核心开发者 / Core Developers
- **[OnyxAxisOwO](https://github.com/OnyxAxisOwO/)** — Creator & Lead Maintainer (项目发起人与核心维护者)
- **[Abloom](https://github.com/abloom25)** — Core Architecture, Frontend Engineering & Performance Optimization (前端架构演进与优化)
- **[amnssb](https://github.com/amnssb)** — Core Feature Contributions & Enhancements (核心功能与组件贡献)

### 协同构建模型 / Co-developed with AI Models
- **Claude Opus 5**
- **GPT 5.6 Sol**
- **Gemini 3.8 Flash**

### 社区致谢 / Community
同时，由衷感谢 **[Axis AI 社区](https://ai.onyxaxis.org)** 各位小伙伴的持续支持、深度测试与宝贵反馈！感谢大家让 Obsidian Arc 变得更稳定、更优雅。

---

## 开源协议 / License

[MIT License](LICENSE)
