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

## 流式与工具调用

开启 `stream` 后返回 Anthropic 事件结构，如 `message_start`、`content_block_start`、`content_block_delta` 和 `message_stop`。

工具请求以 `tool_use` 返回。客户端执行工具后，在下一次消息历史中附上匹配 `tool_use_id` 的 `tool_result`。不要只发送工具结果而省略对应的调用消息。

网关转换工具结构，但不会在服务端执行工具。厂商专用的缓存、签名或其他扩展不保证保留。

## 输入 Token 估算

`POST /v1/messages/count_tokens` 接受同类消息请求，返回：

```json
{"input_tokens": 10}
```

这里的数值仅为格式示例。当前实现进行本地估算，不调用上游官方分词器，不是精确计数，也不能用作最终收费依据。

## 客户端地址

使用会自行拼接 `/v1/messages` 的客户端时，基础地址应填实例根地址，例如 `https://ai.example.com`。如果客户端要求完整端点，则填 `https://ai.example.com/v1/messages`。

不同客户端的设置字段和认证方式可能不同。先确认实际请求落到本页端点，再检查模型权限与工具格式。