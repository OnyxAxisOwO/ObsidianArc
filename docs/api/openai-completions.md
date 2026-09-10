# Chat Completions

`POST /v1/chat/completions` 接受对话历史，返回一次回答或 SSE 流。需要有效 API Key，见[接入与鉴权](./overview)。

## 最小请求

以下 Bash 示例假定已经设置 `OBSIDIAN_API_KEY`：

```bash
curl https://ai.example.com/v1/chat/completions \
  -H "Authorization: Bearer $OBSIDIAN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "your-model",
    "messages": [
      {"role": "user", "content": "用一句话介绍 Go。"}
    ]
  }'
```

把地址替换为自己的实例，把 `your-model` 替换为 `/v1/models` 中的 `id`。非流式回答文字位于 `choices[0].message.content`。

## 支持的参数

| 参数 | 说明 |
| --- | --- |
| `model` | 必填，模型名称 |
| `messages` | 必填，非空消息数组 |
| `stream` | 默认 `false` |
| `temperature` | 可选，未提供时不强制指定 1.0 |
| `max_tokens` | 输出上限，受模型配置限制 |
| `max_completion_tokens` | 与上项同时提供时优先使用 |
| `reasoning_effort` | 推理等级，是否生效取决于模型能力 |
| `tools` | 函数工具定义 |
| `tool_choice` | 工具选择方式 |

请求体上限为 12 MiB，最多 400 条消息和 256 个工具。上游还可能有更小的上下文或参数限制。

## 流式回答

设置 `"stream": true`，并用 `curl -N` 禁用命令行输出缓冲：

```bash
curl -N https://ai.example.com/v1/chat/completions \
  -H "Authorization: Bearer $OBSIDIAN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"your-model","stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

正文增量在 `choices[].delta.content` 中返回。工具调用使用对应的增量字段，客户端需要按调用 ID 组装参数。

## 工具调用

通过 `tools` 声明函数后，模型可以返回 `tool_calls`。工具由客户端执行，网关不会替客户端运行命令或函数。

下一次请求需要包含模型的工具调用消息，以及带有匹配 `tool_call_id` 的 `role: tool` 结果消息。

模型和上游必须支持工具调用。跨协议转换不能保证保留厂商特有的工具扩展。

## 兼容范围

`response_format`、`parallel_tool_calls`、`logprobs`、`n` 和 `seed` 等未实现字段可被接受但忽略，不会自动透传给上游。尤其不能因为请求成功，就认定结构化输出格式约束已经执行。

遇到参数问题时，先使用最小请求验证，再逐项加入可选参数。