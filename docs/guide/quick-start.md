# 首次配置

服务启动后，先添加服务商与模型，再测试网页对话。只有需要外部客户端接入时，才需要开启 API 并创建密钥。

## 添加服务商

1. 使用管理员账号登录，打开 `/admin/providers`。
2. 新建服务商，填写名称、协议类型、Base URL 和上游 API Key。
3. 保存后测试连接，确认地址和凭据有效。

协议类型应与上游实际提供的接口一致。OpenAI 兼容接口使用 `openai`，Anthropic Messages 使用 `anthropic`。Base URL 填服务基础地址，不要填完整的聊天请求路径。

上游密钥只保存在服务端。用户创建的 Obsidian Arc API Key 与这个密钥是两种不同的凭据。

## 添加模型

打开 `/admin/models`，新建模型并选择刚才的服务商。主要字段如下：

| 字段 | 用途 |
| --- | --- |
| 上游模型 ID | 上游实际接受的模型名称 |
| 展示名称 | 网页模型选择器中的名称 |
| API 名称 | 外部客户端调用时使用的名称，可选 |
| 模型能力 | 按实际支持情况配置视觉、推理、工具或生图能力 |

可使用服务商的模型发现功能辅助添加。上游没有实现模型列表接口时，手动填写模型 ID。

确保服务商和模型均已启用。默认组初始允许访问全部模型；如果之后改成按模型授权，还需要给相应用户组勾选模型。

## 完成首次对话

回到首页，选择模型，发送一条简短问题。确认回答能够逐步显示，并且历史会话中能找到刚才的内容。

模型没有出现在列表中时，先检查启用状态和用户组权限。请求失败时，到[请求日志](../admin/logs-and-auditing)查看对应的错误码。

## 为其他用户开放

在 `/admin/users` 创建用户，或在系统设置中开放注册。通过 `/admin/groups` 分配模型权限与额度；组级规则会应用到各成员自己的用量上。

建议用普通账号再测试一次。管理员可以绕过部分分组限制，管理员测试成功不代表普通用户已获得权限。

## 接入 API

1. 在系统设置中开启 `api.enabled`。
2. 为普通用户所属组开启 `api_access`。
3. 以需要调用 API 的账号打开 `/keys`，创建密钥并保存明文。
4. 使用 `/v1/models` 返回的模型名称发起请求。

以下 Bash 示例中，替换 API Key，并将 `your-model` 改为模型列表中的 `id`：

```bash
export OBSIDIAN_API_KEY='替换为你创建的 API Key'

curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer $OBSIDIAN_API_KEY"

curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $OBSIDIAN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"your-model","stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

`/api/chat` 是网页会话接口，需要登录 Cookie。外部客户端应使用上面的 `/v1/chat/completions`。

更多协议和限制见 [API 接入与鉴权](../api/overview)。