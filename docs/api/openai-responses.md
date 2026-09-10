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

本端点读取 `max_output_tokens`，不要用 Chat Completions 的 `max_tokens` 替代。

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

工具定义采用 Responses 的结构，函数名称与参数直接放在工具项中，不要照搬 Chat Completions 的 `function` 嵌套格式。

## 不支持的能力

`store: true` 和非空 `previous_response_id` 会被拒绝。不能依赖服务端通过上次响应 ID 恢复上下文。

当前没有后台任务、响应查询或删除接口，也没有内置网页搜索、文件检索或代码执行服务。客户端额外发送字段，不代表对应能力已实现。

调用支持此格式的客户端时，确认它可以关闭服务端存储，并由客户端维护历史。