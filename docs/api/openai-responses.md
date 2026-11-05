# Responses

`POST /v1/responses` 提供 Responses 格式的输入、输出和流式事件。网关按无状态方式处理，不保存可在后续请求中引用的响应对象。

## 请求示例

```bash
curl https://ai.example.com/v1/responses \
  -H "Authorization: Bearer $OBSIDIAN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "your-model",
    "store": false,
    "instructions": "回答简洁。",
    "input": "介绍一下 Go 的 channel。",
    "max_output_tokens": 1024
  }'
```

`input` 可以是字符串，也可以是包含消息、函数调用与函数结果的数组。

## 参数

| 参数 | 说明 |
| --- | --- |
| `model` | 模型名称 |
| `input` | 必须提供有效输入 |
| `instructions` | 系统指令 |
| `stream` | 默认 `false` |
| `max_output_tokens` | 输出上限，受模型配置约束 |
| `temperature` | 可选采样参数 |
| `reasoning.effort` | 推理等级 |
| `tools` / `tool_choice` | 受支持的函数工具与选择方式 |
| `store` | 省略或设为 `false` |
| `text.format` | `text`、`json_object` 或 `json_schema` 结构化输出 |
| `parallel_tool_calls` | 并行工具调用控制 |
| `prompt_cache_key` / `prompt_cache_retention` | 转发到 OpenAI 上游的缓存提示 |

本端点读取 `max_output_tokens`，不要用 Chat Completions 的 `max_tokens` 替代。

请求体上限为 12 MiB，最多 1024 个受支持的函数工具。网关不另设 `input` 数组条目数上限；上游仍可能有消息数量、上下文或参数限制。

## 返回内容

非流式结果使用 `object: "response"` 和 `output` 数组。文本位于 message 项的 content 中，类型为 `output_text`。

以下只展示用于读取文本的结构片段：

```json
{
  "object": "response",
  "output": [{
    "type": "message",
    "role": "assistant",
    "content": [{
      "type": "output_text",
      "text": "这里是模型回答。",
      "annotations": []
    }]
  }]
}
```

流式正文通过 `response.output_text.delta` 等事件提供。函数调用使用独立的输出项，不能把所有 `output` 项都当成文本。

## 多轮与工具结果

每次请求都要携带所需的完整历史。函数调用结果通过 `function_call_output` 输入项返回，使用 `call_id` 与先前调用对应。

工具定义采用 Responses 的结构，函数名称与参数直接放在工具项中，不要照搬 Chat Completions 的 `function` 嵌套格式。并行调用在上游重建为同一助手回合；命名空间展开后的工具名称必须唯一。不同命名空间中的同名工具会被拒绝，避免调用错误的函数。

`usage.output_tokens` 包含 `output_tokens_details.reasoning_tokens`，后者是子集。缓存读取和兼容上游的写入统计位于 `input_tokens_details`。上游流损坏或提前结束时返回 `response.failed`，不会报告 `response.completed`。

## 不支持的能力

`store: true`、非空 `previous_response_id`、非空 `conversation` 和 `background: true` 都会被拒绝。不能依赖服务端通过上次响应 ID 或会话 ID 恢复上下文。

`function_call_output` 的输出只能包含文本。其中的图片会被拒绝，而不是被静默丢弃；请把图片放在用户消息中发送。

当前没有后台任务、响应查询或删除接口，也没有内置网页搜索、文件检索或代码执行服务。客户端额外发送字段，不代表对应能力已实现。

调用支持此格式的客户端时，确认它可以关闭服务端存储，并由客户端维护历史。

## 拒答与托管工具边界

模型的显式拒答使用 `type: "refusal"` 内容项，流式事件为 `response.refusal.delta` / `response.refusal.done`，完成对象中保留完整拒答文字。厂商错误诊断仍按错误响应处理。

网关不执行厂商托管工具。为兼容默认携带这些声明的客户端，自动选择模式下仍过滤托管工具声明；这不表示工具可用。显式要求调用托管工具或没有受支持函数却指定 `required` 时，会在调用和计费前返回错误。上游的 `pause_turn` 需要本接口不支持的托管工具续传，会返回失败，而不是报告完成。

文本块、系统消息和函数定义上的 Anthropic 风格 `cache_control` 会保留给支持该扩展的上游；也可发送顶层自动缓存控制。它与 OpenAI 原生的 `prompt_cache_key` / `prompt_cache_retention` 是不同的可选控制。
