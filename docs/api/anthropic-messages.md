# Anthropic Messages 接口与 Claude Code

`POST /v1/messages` 遵循 Anthropic Messages API 协议规范，支持 Anthropic 官方 Python / TypeScript SDK，并支持 Claude Code 命令行编程工具对接。

---

## 接口规范

- **请求方法**：`POST`
- **请求地址**：`https://<你的域名>/v1/messages`
- **认证方式**：
  - HTTP Header `x-api-key: sk-oa-<你的APIKey>` 或
  - HTTP Header `Authorization: Bearer sk-oa-<你的APIKey>`
- **协议版本标头**：`anthropic-version: 2023-06-01`

### 请求体参数

| 参数名 | 类型 | 必填 | 说明 |
| :--- | :--- | :--- | :--- |
| `model` | string | **是** | 模型名称或模型别名（如 `claude-3-7-sonnet-20250219`）。 |
| `messages` | array | **是** | 消息数组（元素角色仅允许 `user` 与 `assistant`）。 |
| `system` | string/array | 否 | 系统指令（Anthropic 规范中系统提示词作为独立参数传入）。 |
| `max_tokens` | int | **是** | 最大生成 Token 数上限。 |
| `stream` | bool | 否 | 是否启用 SSE 流式传输。 |
| `thinking` | object | 否 | 思考参数（如 `{"type": "enabled", "budget_tokens": 4096}`）。 |
| `tools` | array | 否 | 工具定义列表（遵循 Anthropic Tool Use 规范）。 |

---

## 接入 Claude Code 命令行工具

Claude Code 默认直接向 Anthropic Messages 协议通信。通过配置环境变量可将请求路由至 Obsidian Arc 实例：

### 1. 配置环境变量

```bash
# 设置网关地址
export ANTHROPIC_BASE_URL="https://ai.example.com"

# 设置从 Obsidian Arc 申请的 API Key
export ANTHROPIC_API_KEY="sk-oa-xxxxxxxxxxxxxxxxxxxx"
```

### 2. 运行 Claude Code

```bash
claude
```

---

## 跨协议工具调用适配 (Tool Calling Translation)

### 做了什么
当客户端通过 `/v1/messages` 发起遵循 Anthropic 规范的请求，但目标模型底层挂载的是 OpenAI 规范的上游服务商时，网关执行以下双向转换：
1. 将 Anthropic 的 `system`、`messages` 与 `tools`（含 `input_schema`）转换为 OpenAI 兼容的请求体；
2. 将上游流式返回的 `tool_calls` 事件重构为 Anthropic 的 `content_block_start`（`type: tool_use`）与 `content_block_delta` 事件流；
3. 当客户端返回 `tool_result` 类型的用户消息时，转换为 OpenAI 规范的 `role: tool` 消息继续向上游发送。

```text
Claude Code CLI (发出 Anthropic messages + tool_use)
                           │
                           ▼
          Obsidian Arc 网关 (/v1/messages)
                           │
    底层服务商为 OpenAI 兼容模型？
         ├── 是  ──► 转换为 OpenAI tool_calls 请求上游
         └── 否  ──► 直通 Anthropic 上游
                           │
                           ▼
       接收响应并转换回 Anthropic content_block: tool_use
                           │
                           ▼
Claude Code 执行本地命令后回传 tool_result ──► 网关转换为 tool 消息
```

### 为什么这么做
使调用方能够使用单一的 Anthropic 客户端或专有 CLI 工具（如 Claude Code），自由选用不同后端服务商提供的模型，无需受限于单一服务商。

### 代价是什么
1. **结构转换存在延迟**：流式转换需要缓冲部分 Token 以组装合法的事件结构，增加了首字延迟（TTFT）；
2. **Schema 兼容限制**：若工具定义的 JSON Schema 包含特定厂商不支持的高级关键字，上游模型可能在解析时报错或不予执行；
3. **思考模型格式差异**：不同模型在同时输出思考过程与工具调用时的顺序与标签规范不同，极端情况下可能出现工具参数提取失败。

---

## Python SDK 调用示例

```python
import anthropic

client = anthropic.Anthropic(
    base_url="https://ai.example.com",
    api_key="sk-oa-xxxxxxxxxxxx",
)

message = client.messages.create(
    model="claude-3-7-sonnet-20250219",
    max_tokens=1024,
    messages=[
        {"role": "user", "content": "简述 Go 语言中的 channel 原理。"}
    ]
)

print(message.content[0].text)
```

---

## Token 预估端点 (`POST /v1/messages/count_tokens`)

支持在发送完整生成请求前计算输入消息的 Token 数量：

```bash
curl https://ai.example.com/v1/messages/count_tokens \
  -H "x-api-key: sk-oa-xxxxxxxxxxxx" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-7-sonnet-20250219",
    "messages": [{"role": "user", "content": "Hello, world!"}]
  }'
```

返回示例：
```json
{
  "input_tokens": 10
}
```

---

## 运维约束与排查路径

### 失效场景
1. **`anthropic-version` 缺失或版本不支持**：若未携带版本头或上游要求特定版本，可能导致某些新特性参数被上游忽略；
2. **Claude Code 报 400 Bad Request**：检查是否给仅支持文本的模型配置了思维链参数；或检查工具回传的消息中是否存在空 content。

### 不明显的约束与前提
1. **工具调用 ID 双向映射**：客户端（如 Claude Code CLI）回传 `tool_result` 时必须包含 `tool_use_id`，网关在转交 OpenAI 兼容上游时需严格映射为 `tool_call_id`，缺少 ID 会直接导致上游 400 报错；
2. **鉴权头双重支持**：接口同时兼容 `x-api-key: sk-oa-...` 与 `Authorization: Bearer sk-oa-...` 两种标头格式，但前提是全站开启 API 总开关且所属用户组开启 `api_access`；
3. **系统提示词独立传递**：Anthropic 规范将 System Prompt 作为独立的 `system` 顶层字段传递，若底层映射到旧版 OpenAI 兼容模型，网关会自动将其前置包装为 `role: system` 消息。

### 排查步骤
1. **查看转换流水**：在管理后台「请求日志」中找到该请求，查看网关实际发送给上游服务商的请求体与上游原始返回；
2. **排查 Token 超限**：若报错包含 `max_tokens`，核对请求参数中的 `max_tokens` 是否超出了模型支持的最大单次输出上限。
