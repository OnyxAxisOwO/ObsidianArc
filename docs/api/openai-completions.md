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
| `parallel_tool_calls` | 是否允许并行工具调用 |
| `stop` | 字符串或最多 4 项的停止词数组 |
| `response_format` | `text`、`json_object` 或 `json_schema`，仍需上游模型支持 |
| `prompt_cache_key` / `prompt_cache_retention` | 转发到 OpenAI 上游的缓存提示 |
| `stream_options.include_usage` | 设为 `true` 时增加 `choices: []` 的独立用量块 |

请求体上限为 12 MiB，最多 1024 个工具。网关不另设消息条数上限；上游仍可能有消息数量、上下文或参数限制。

## 流式回答

设置 `"stream": true`，并用 `curl -N` 禁用命令行输出缓冲：

```bash
curl -N https://ai.example.com/v1/chat/completions \
  -H "Authorization: Bearer $OBSIDIAN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"your-model","stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

正文增量在 `choices[].delta.content` 中返回。工具调用使用对应的增量字段，客户端需要按调用 ID 组装参数。用量只在请求 `stream_options.include_usage: true` 时返回，位于完成块之后、`[DONE]` 之前的独立块中。

上游提前断开、数据帧损坏或超过配置的空闲时限会返回错误，不能把部分内容当成完整回答。流中可能出现保持连接的 SSE 注释。

`completion_tokens` 包含 `completion_tokens_details.reasoning_tokens`，两者不能相加。`prompt_tokens_details.cached_tokens` 和兼容上游的 `cache_write_tokens` 均为 `prompt_tokens` 的子集。

## 工具调用

通过 `tools` 声明函数后，模型可以返回 `tool_calls`。工具由客户端执行，网关不会替客户端运行命令或函数。

下一次请求需要包含模型的工具调用消息，以及带有匹配 `tool_call_id` 的 `role: tool` 结果消息。`role: tool` 消息只能包含文本；其中的图片会被拒绝，请改在用户消息中发送。

模型和上游必须支持工具调用。跨协议转换不能保证保留厂商特有的工具扩展。

## 兼容范围

`logprobs`、`n` 和 `seed` 等未实现字段可被接受但忽略，不会自动透传给上游。结构化输出和停止词会转换为上游对应控制；不支持的格式会被拒绝。原生 Anthropic 上游接受 `json_schema`，不接受没有 schema 的 `json_object`。

工具的 `strict` 会随定义转发。文本块和工具上的 `cache_control` 会保留给支持该扩展的兼容上游。实际缓存命中由上游决定。

遇到参数问题时，先使用最小请求验证，再逐项加入可选参数。

## 拒答与托管工具

模型生成的拒答文字会保留在 `message.refusal` 或流式 `delta.refusal`，不会被替换成空回答或通用错误。提供商的错误诊断仍按 API 错误处理，不会直接暴露给客户端。助手历史中的 `refusal` 也会保留为上下文。

此接口只支持客户端函数工具；厂商托管工具会返回明确错误。`tool_choice` 要求调用未声明的函数或没有受支持函数可调用时，也会在调用和计费前拒绝。`reasoning_effort: "xhigh"` 会保留给支持它的上游。
