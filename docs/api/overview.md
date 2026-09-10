# 网关概述与鉴权

Obsidian Arc 通过 `/v1` 路径对外暴露统一的 AI API 网关，支持外部客户端、脚本与智能体调用。本文档说明网关的鉴权机制、设计约束与可用端点。

---

## 核心设计规范

网关在设计上执行以下三项规则：

### 1. 无状态数据处理 (Stateless)

#### 做了什么
客户端在请求体中提交完整的对话上下文历史。API 网关处理请求时不向数据库写入会话记录（`conversations`）、消息记录（`messages`）或附件，仅执行配额校验并向 `usage_records` 写入计费与审计账本。

#### 为什么这么做
API 调用通常来自自动化脚本、CI/CD 流水线或代码补全插件，高频生成大量的短期中间请求。若将每次 API 调用均作为会话持久化，会快速膨胀数据库体积并产生大量无用历史记录。

#### 代价是什么
客户端必须自行维护上下文历史并在后续请求中完整传输，增加了请求上行的数据量；服务端无法提供针对 API 会话的云端历史回放功能。

---

### 2. 仅基于 Token 鉴权与 Cookie 隔离

#### 做了什么
网关端点仅接受 HTTP 请求头 `Authorization: Bearer sk-oa-...` 传入的 API Key。`/v1/*` 接口完全忽略请求中携带的浏览器 Cookie（包括 `obsidian_session`）。

#### 为什么这么做
防止恶意网站通过跨站请求伪造（CSRF）诱导已登录用户的浏览器向 `/v1` 发送未经授权的 API 调用。

#### 代价是什么
在基于浏览器的轻量脚本中调用网关必须显式在请求头中配置 API Key，无法复用已有的用户登录态 Cookie。

---

### 3. 上游凭据与拓扑脱敏

#### 做了什么
网关统一拦截上游服务商的返回数据，对响应体进行解析和字段重构后返回给调用方。过滤上游真实的 Base URL、专有 Headers、内部节点 ID 与原始签名信息。

#### 为什么这么做
保护服务商敏感凭据与网络架构，使调用方仅感知到 Obsidian Arc 实例暴露的统一规范。

#### 代价是什么
服务端必须对上游返回的 JSON 数据或 SSE 数据块进行反序列化与字段过滤，带来了微秒级的内存分配与 CPU 解析开销。

---

## API Key 凭据管理

1. 登录用户界面，进入「API 密钥」面板；
2. 点击「新建 API Key」；
3. 输入密钥名称与绑定的模型范围（可选）；
4. 创建后获取以 `sk-oa-` 开头的密钥字符串（明文仅在创建时展示一次）。

---

## 客户端接入配置

| 客户端 / 工具 | 协议规范 | 基础地址配置 (Base URL) | 说明 |
| :--- | :--- | :--- | :--- |
| **Claude Code CLI** | Anthropic Messages | `ANTHROPIC_BASE_URL=https://ai.example.com` | 支持 Anthropic 协议与工具调用。 |
| **Cursor / Continue** | OpenAI Completions | `https://ai.example.com/v1` | 用于代码补全与编辑区对话。 |
| **Cherry Studio / NextChat** | OpenAI Completions | `https://ai.example.com/v1` | 桌面与 Web 客户端，支持模型列表获取。 |
| **OpenAI SDK (Python / TS)** | OpenAI Completions | `base_url="https://ai.example.com/v1"` | 官方 SDK 直接调用。 |
| **Codex / Agent 工具** | OpenAI Responses | `https://ai.example.com/v1/responses` | 结构化指令输出标准。 |

---

## 统一端点清单

- `GET /v1/models`：获取当前 API Key 具备访问权限的模型列表；
- `POST /v1/chat/completions`：OpenAI 对话补全接口（支持流式与 Tool Calling）；
- `POST /v1/messages`：Anthropic 消息接口（支持思考模式与工具调用）；
- `POST /v1/messages/count_tokens`：Anthropic 协议 Token 统计接口；
- `POST /v1/responses`：OpenAI Responses 规范端点；
- `POST /v1/images/generations`：图像生成接口。

---

## 运维约束与排查路径

### 失效场景
1. **全站 API 处于关闭状态**：若管理后台「系统设置」中 `api.enabled` 为 `false`，任何非管理员的 API Key 调用均会直接返回 `403 Forbidden`（错误码 `api_disabled`）；
2. **用户组未开放 API 权限**：若用户所属用户组的 `api_access` 开关处于关闭状态，该用户的 API Key 同样返回 403；
3. **缺少 Bearer 前缀**：若请求头填写为 `Authorization: sk-oa-...` 缺少 `Bearer ` 前缀，鉴权中间件会判定格式非法并返回 401。

### 不明显的约束与前提
1. **严格无状态鉴权**：`/v1/*` 端点完全忽略请求中携带的任何浏览器 Cookie，强制仅基于 HTTP Header 中的 Token 进行认证；
2. **凭证仅展示一次**：API Key 明文在生成后由后端通过哈希存储（仅保存前缀与哈希值），一旦丢失无法在后台找回明文，只能作废并重新创建；
3. **代理真实 IP 依赖**：网关的单 IP 频控依赖客户端真实 IP，在 Nginx 或 Caddy 之后必须配置 `OBSIDIAN_TRUST_PROXY=true` 并限定信任网段，避免全站调用共用单个代理 IP 频控。

### 排查步骤
1. **鉴权失败核查**：在管理后台「请求日志」筛选状态码 401 与 403，核对具体的错误码提示；
2. **Key 状态确认**：在用户「API 密钥」面板检查该 Key 是否处于已撤销状态。
