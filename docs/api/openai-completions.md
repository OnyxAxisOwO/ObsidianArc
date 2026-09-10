# OpenAI Completions 兼容接口

`POST /v1/chat/completions` 端点遵循 OpenAI Chat Completions API 协议规范，支持非流式响应、Server-Sent Events (SSE) 流式推流与工具调用（Tool / Function Calling）。

---

## 接口规范

- **请求方法**：`POST`
- **请求地址**：`https://<你的域名>/v1/chat/completions`
- **认证方式**：HTTP Header `Authorization: Bearer sk-oa-<你的APIKey>`

### 请求体参数

| 参数名 | 类型 | 必填 | 默认值 | 说明 |
| :--- | :--- | :--- | :--- | :--- |
| `model` | string | **是** | — | 模型标识（Model ID 或模型别名 `api_name`，如 `gpt-4o`、`deepseek-chat`）。 |
| `messages` | array | **是** | — | 消息历史列表，元素包含 `role` (`system` / `user` / `assistant` / `tool`) 与 `content`。 |
| `stream` | bool | 否 | `false` | 是否开启 SSE 打字机流式推流。 |
| `temperature` | number | 否 | `1.0` | 采样温度（`0.0 ~ 2.0`）。 |
| `max_tokens` | int | 否 | — | 最大生成 Token 数上限。 |
| `tools` | array | 否 | — | 可供模型调用的函数工具列表。 |
| `tool_choice` | string/obj | 否 | `auto` | 指定工具调用策略（如 `auto` 或 `none`）。 |
| `reasoning_effort` | string | 否 | — | 针对推理思考模型的思考预算等级（`low` / `medium` / `high`）。 |

---

## 代码调用示例

### 1. cURL 命令行调用（流式）

```bash
curl https://ai.example.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-oa-xxxxxxxxxxxx" \
  -d '{
    "model": "deepseek-chat",
    "stream": true,
    "messages": [
      {"role": "system", "content": "你是一位系统工程师。"},
      {"role": "user", "content": "请简述进程与协程的区别。"}
    ]
  }'
```

### 2. Python 官方 SDK 调用

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://ai.example.com/v1",
    api_key="sk-oa-xxxxxxxxxxxx",
)

response = client.chat.completions.create(
    model="deepseek-chat",
    messages=[
        {"role": "user", "content": "写一个 Go 语言并发示例。"},
    ],
    stream=False,
)

print(response.choices[0].message.content)
```

### 3. Node.js / TypeScript 调用

```typescript
import OpenAI from 'openai';

const client = new OpenAI({
  baseURL: 'https://ai.example.com/v1',
  apiKey: 'sk-oa-xxxxxxxxxxxx',
});

async function main() {
  const stream = await client.chat.completions.create({
    model: 'gpt-4o',
    messages: [{ role: 'user', content: '列出三种常见的数据结构。' }],
    stream: true,
  });

  for await (const chunk of stream) {
    process.stdout.write(chunk.choices[0]?.delta?.content || '');
  }
}

main();
```

---

## 工具调用转换机制 (Tool Calling)

### 做了什么
当上游服务商采用非 OpenAI 协议（例如底层为 Anthropic 协议模型）时，网关在入站阶段将 OpenAI `tools` 参数转换为 Anthropic `tool` 结构；在收到上游模型的 `tool_use` 事件时，转换为标准的 OpenAI `tool_calls` 结构返回给调用方。

### 为什么这么做
允许开发者使用标准 OpenAI 客户端库调用底层挂载了各类异构上游服务商的模型，无需在客户端针对每个模型编写专有的适配逻辑。

### 代价是什么
不同厂商在工具描述的 JSON Schema 严格程度、嵌套深度和工具调用结果的返回要求上存在细微差异。转换可能丢失厂商特有的非标参数（如特定缓存标记）。

```json
{
  "id": "chatcmpl-01J...",
  "object": "chat.completion",
  "created": 1726000000,
  "model": "gpt-4o",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": null,
        "tool_calls": [
          {
            "id": "call_abc123",
            "type": "function",
            "function": {
              "name": "get_current_weather",
              "arguments": "{\"location\": \"Beijing\", \"unit\": \"celsius\"}"
            }
          }
        ]
      },
      "finish_reason": "tool_calls"
    }
  ]
}
```

---

## 错误码对照表

请求发生异常时，接口返回符合 OpenAI 规范的 JSON 错误载荷：

| HTTP 状态码 | 错误类型 (`type`) | 错误代码 (`code`) | 说明与排查路径 |
| :--- | :--- | :--- | :--- |
| `401 Unauthorized` | `invalid_request_error` | `invalid_api_key` | API Key 未提供、格式错误或已被删除/禁用。 |
| `403 Forbidden` | `invalid_request_error` | `api_disabled` | 全站 API 处于关闭状态，或该用户组未开启 `api_access`。 |
| `403 Forbidden` | `invalid_request_error` | `email_unverified` | 实例启用了强制邮箱验证，当前账号尚未完成邮件激活。 |
| `404 Not Found` | `invalid_request_error` | `model_not_found` | 请求的模型不存在，或当前 API Key 无权访问该模型。 |
| `429 Too Many Requests` | `invalid_request_error` | `too_many_in_flight` | 该账户并发处理中的请求数已达到单账号上限。 |
| `429 Too Many Requests` | `invalid_request_error` | `quota_exceeded` | 5 小时、日、周或月度额度耗尽，需等待窗口滚动或使用兑换卡。 |
| `500 Internal Error` | `server_error` | `internal_error` | 上游服务商网络异常或返回不可解析数据。 |

---

## 运维约束与排查路径

### 失效场景
1. **上游不支持流式 Tool Calling**：部分较旧的模型在开启 `stream: true` 时无法返回规范的流式工具调用片段，会导致客户端解析 JSON 参数中断；
2. **多轮 Tool Calling 消息缺失**：在回复 `role: tool` 消息时，若缺少对应的 `tool_call_id`，上游模型会返回 400 校验错误。

### 不明显的约束与前提
1. **双重鉴权门禁约束**：调用本接口除持有有效 Key 外，全局设置 `api.enabled` 必须开启，且 Key 拥有者所在用户组必须开启 `api_access` 门禁，否则返回 403；
2. **两阶段额度预扣限制**：请求进入时按模型配置的最大输出 Token 进行最坏情况预扣，若账户当前额度低于该预扣值，即便实际仅生成单个 Token，请求也会被直接 429 拦截；
3. **反向代理无缓冲要求**：使用 Nginx 等反代时必须关闭缓冲（`proxy_buffering off`），否则 SSE 流式传输会被反代积攒成大块后集中推送，破坏打字机体验。

### 排查步骤
1. **错误定位**：在管理后台「请求日志」中找到该次请求的全局 ID，查看原始错误返回体；
2. **验证模型可用性**：在管理后台「模型管理」中核对该模型的启用状态和真实上游 ID。
