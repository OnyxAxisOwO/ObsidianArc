# Anthropic Messages

`POST /v1/messages` 接受 Anthropic 风格的消息和工具结构。上游可以是已配置的 Anthropic 服务商，也可以由适配层转换到其他受支持协议。

## 请求示例

```bash
curl https://ai.example.com/v1/messages \
  -H "x-api-key: $OBSIDIAN_API_KEY" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "your-model",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "用一句话介绍 Go。"}
    ]
  }'
```

也可以使用 `Authorization: Bearer`。地址、密钥与模型名都使用本实例的配置。

## 参数

| 参数 | 说明 |
| --- | --- |
| `model` | 模型名称 |
| `messages` | 用户与助手的消息历史 |
| `system` | 独立的系统提示词，字符串或文本块数组 |
| `max_tokens` | 输出 Token 上限，调用时应明确提供 |
| `stream` | 默认 `false` |
| `temperature` | 可选采样参数 |
| `thinking` | 思考配置，需模型支持 |
| `tools` / `tool_choice` | 工具定义与选择方式 |
| `stop_sequences` | 自定义停止词 |
| `cache_control` | 自动缓存控制；system、内容块和工具上的缓存断点也会保留 |
| `output_config.format` | `json_schema` 结构化输出 |

请求体上限为 12 MiB，最多 1024 个工具。网关不另设消息条数上限；上游仍可能有消息数量、上下文或参数限制。

## 流式与工具调用

开启 `stream` 后返回 Anthropic 事件结构，如 `message_start`、`content_block_start`、`content_block_delta` 和 `message_stop`。

工具请求以 `tool_use` 返回。客户端执行工具后，在下一次消息历史中附上匹配 `tool_use_id` 的 `tool_result`。不要只发送工具结果而省略对应的调用消息。`tool_result` 的内容只能是文本。其中的图片会被拒绝，而不是被静默丢弃；请把图片放在用户消息中发送。

网关转换工具结构，但不会在服务端执行工具。缓存控制支持 `{"type":"ephemeral"}` 及 `ttl: "5m"` / `"1h"`。断点顺序会保留；实际缓存仍受上游模型、最小长度和断点数量限制。签名及其他未实现的扩展不保证保留。`tool_result.is_error` 会保留；转到 OpenAI 上游时以工具结果中的错误标记文本表达。

`usage.input_tokens` 是未缓存输入，缓存读取和写入分别位于 `cache_read_input_tokens` 和 `cache_creation_input_tokens`，总输入应合计三项。原生上游的初始用量会进入 `message_start`；若上游只在结束时报告用量，初始输入为本地估算，最终 `message_delta.usage` 给出更新统计。停止词和 `stop_sequence` 会保留，上下文耗尽也会标明。

思考预算受最终输出上限限制，不会抬高 `max_tokens`；上限放不下最小思考预算时关闭该次请求的思考。损坏或中断的上游流会返回 `error`，不会再发表示成功完成的 `message_stop`。

## 输入 Token 估算

`POST /v1/messages/count_tokens` 接受同类消息请求，返回：

```json
{"input_tokens": 10}
```

这里的数值仅为格式示例。当前实现进行本地估算，不调用上游官方分词器，不是精确计数，也不能用作最终收费依据。

## 客户端地址

使用会自行拼接 `/v1/messages` 的客户端时，基础地址应填实例根地址，例如 `https://ai.example.com`。如果客户端要求完整端点，则填 `https://ai.example.com/v1/messages`。

不同客户端的设置字段和认证方式可能不同。先确认实际请求落到本页端点，再检查模型权限与工具格式。

## 明确不支持的结果与工具

工具结果目前只支持文本：图片、嵌套工具结果和放在结果内部文本块上的缓存断点会被拒绝。缓存断点应放在外层 `tool_result` 上，避免被文本转换丢弃。

网关不执行厂商托管工具。自动选择模式下仍过滤这些声明，以兼容默认发送它们的客户端；强制调用它们或指定不存在的客户端函数时，会在调用和计费前返回错误。模型已生成的拒答文本会随 `stop_reason: "refusal"` 返回，其他提供商错误仍以错误事件报告。

Messages 不会伪造带签名的 thinking 块。若上游仅返回 reasoning、没有正文或工具调用，本接口会明确失败并保留实际用量；需要读取这类结果时请使用 Chat Completions 或 Responses。
