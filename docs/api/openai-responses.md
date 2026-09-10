# OpenAI Responses 接口 (Codex 规范)

`POST /v1/responses` 遵循 OpenAI 面向代码辅助工具与智能体推出的 Responses 协议规范。

---

## 接口规范

- **请求方法**：`POST`
- **请求地址**：`https://<你的域名>/v1/responses`
- **认证方式**：HTTP Header `Authorization: Bearer sk-oa-<你的APIKey>`

### 请求体参数

| 参数名 | 类型 | 必填 | 说明 |
| :--- | :--- | :--- | :--- |
| `model` | string | **是** | 模型名称或别名。 |
| `input` | string/array | **是** | 待处理的上下文或用户输入内容。 |
| `instructions` | string | 否 | 系统指令说明（相当于 System Prompt）。 |
| `stream` | bool | 否 | 是否启用流式输出。 |
| `tools` | array | 否 | 结构化工具定义。 |

---

## 协议适配机制

### 做了什么
网关接收符合 Responses 规范的入站请求，将其映射到底层模型的调用格式（如 Completions 或原生上游结构），并在输出端将生成结果统一重构成结构化的 `output` 数组对象。

### 为什么这么做
支持使用新一代代码辅助插件或基于 Responses 格式编写的自动化工具直接接入，使开发者无需改造客户端即可调用已有模型。

### 代价是什么
Responses 格式相比基础 Completions 增加了结构包装，输入与输出对象的序列化过程会带来微小的性能开销；对于不支持直接生成结构化消息的旧模型，必须通过提示词与后置转换模拟该协议。

---

## 调用示例

### cURL 请求

```bash
curl https://ai.example.com/v1/responses \
  -H "Authorization: Bearer sk-oa-xxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "instructions": "你是一个代码审查工具。",
    "input": "func Sum(a, b int) int { return a + b }"
  }'
```

### 响应示例

```json
{
  "id": "resp_01J...",
  "object": "response",
  "created": 1726000000,
  "model": "gpt-4o",
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "代码实现简洁规范，无并发安全风险。"
        }
      ]
    }
  ],
  "usage": {
    "input_tokens": 18,
    "output_tokens": 12,
    "total_tokens": 30
  }
}
```

---

## 运维约束与排查路径

### 失效场景
1. **上游模型无法映射指令参数**：若上游服务商不支持独立的 system/instruction 区分，系统会将 `instructions` 与 `input` 合并为多轮消息，可能轻微改变模型原本的注意力分配；
2. **流式分块协议不兼容**：客户端若依赖专有的 Responses 流式事件类型，而网关转换逻辑未能覆盖某些未公开的增量事件，会导致流式输出中断。

### 不明显的约束与前提
1. **结构化参数必填要求**：该端点要求请求体必须携带 `input` 字段，且对于包含 `instructions` 的请求，底层适配器会根据上游模型能力进行指令重构；
2. **用量记账一致性**：Responses 规范返回的 `output` 数组会被解析并折算为统一的 Token 账本记录，遵循相同的滑动窗口配额策略；
3. **模型透明路由适用**：本端点同样支持后台配置的模型透明重定向，但要求目标模型具备文本生成能力。

### 排查步骤
1. **核对参数结构**：在管理后台「请求日志」中查看接收到的原始 JSON 请求体，确认 `input` 与 `instructions` 字段是否存在反序列化异常；
2. **检查模型绑定权限**：核对 API Key 是否具备调用该指定 `model` 的权限。
