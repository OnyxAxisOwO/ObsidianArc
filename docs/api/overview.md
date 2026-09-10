# 接入与鉴权

Obsidian Arc 在 `/v1` 提供兼容 API，供脚本和客户端调用已配置的模型。网页使用的 `/api` 会话接口与它分开。

## 准备 API Key

管理员先开启全站 `api.enabled`，并为普通用户组开启 `api_access`。然后登录对应账号，在 `/keys` 创建密钥。

密钥明文只在创建时提供，丢失后需要重新创建。密钥可以绑定一个或多个模型，也可以不绑定；具体调用仍受账号权限约束。

## 请求地址与认证

OpenAI 兼容客户端的基础地址通常填 `https://ai.example.com/v1`。原始 HTTP 请求则填完整的端点路径，不要把基础地址和完整路径重复拼接。

推荐使用：

```http
Authorization: Bearer YOUR_API_KEY
Content-Type: application/json
```

未提供 `Authorization` 时，也接受 `x-api-key`。同时提供两者时优先处理 `Authorization`，因此不要保留一份无效的 Authorization 头再期望另一份密钥生效。

登录 Cookie 不能替代 API Key。用户密钥也不能调用需要登录会话的管理接口。

## 可用端点

| 方法与路径 | 用途 |
| --- | --- |
| `GET /v1/models` | 当前密钥可用的模型列表 |
| `GET /v1/models/{id}` | 单个模型信息 |
| `POST /v1/chat/completions` | Chat Completions |
| `POST /v1/messages` | Anthropic Messages |
| `POST /v1/messages/count_tokens` | 输入 Token 估算 |
| `POST /v1/responses` | Responses |
| `POST /v1/images/generations` | 图像生成 |

先通过[模型列表](./models)获取名称，再发起生成请求。示例中的 `your-model` 都需要替换成实际返回的模型 `id`。

## 会话与用量

兼容 API 不创建网页历史会话、消息或附件。多轮对话由客户端维护，并在每次请求中提交所需历史。

调用仍会写入用量记录，并与网页共用账号额度。服务端根据适配后的事件构造响应，不保证透传上游的全部字段或厂商扩展。

## 常见错误

| HTTP 状态 | 错误码 | 含义 |
| --- | --- | --- |
| 404 | `api_disabled` | 全站 API 已关闭，包括管理员调用 |
| 401 | `invalid_api_key` | 缺少或无效的密钥，也可能是账号或用户组不允许调用 |
| 401 | `api_key_paused` | 密钥已暂停 |
| 404 | `model_not_found` | 模型名称不可解析或不在可用范围 |
| 403 | `model_not_permitted` | 请求不在密钥绑定的模型范围内 |
| 403 | `email_unverified` | 当前生效的邮箱验证要求尚未完成 |
| 429 | 见响应中的错误码 | 额度、频率或并发限制 |

不同协议的错误包结构不同。流式请求已开始后，错误也可能通过流中的事件返回，不能只检查最初的 HTTP 状态。

各端点只实现文档中明确列出的能力。“兼容”不意味着支持对应平台的全部 API。