# 模型列表

通过模型列表获取当前密钥可以调用的名称，再把返回的 `id` 填入生成请求。

## 获取列表

```bash
curl https://ai.example.com/v1/models \
  -H "Authorization: Bearer $OBSIDIAN_API_KEY"
```

返回结构示例：

```json
{
  "object": "list",
  "data": [{
    "id": "your-model",
    "object": "model",
    "created": 1788998400,
    "owned_by": "obsidian-arc",
    "display_name": "示例模型"
  }]
}
```

`display_name` 和 `description` 是可选的补充字段。客户端调用时使用 `id`，不要用展示名称替代。

## 模型名称

配置了 `api_name` 时，对外名称优先使用该值；未配置时使用模型的 API 命名规则。最可靠的方法是直接使用列表返回的名称。

`owned_by` 固定为 `obsidian-arc`，不表示上游服务商名称。

## 查询单个模型

```http
GET /v1/models/{id}
Authorization: Bearer YOUR_API_KEY
```

路径中的模型名称需要正确进行 URL 编码。不存在或不在可用范围内的模型会返回错误。

## 可见范围

列表受账号、用户组、模型启用与隐藏状态，以及密钥的模型绑定共同约束。绑定模型范围的密钥不会因此获得范围之外的模型访问权。

模型列表不是全站管理目录，也不应被用来判断后台配置了多少个服务商。

## 返回空列表时

检查服务商和模型是否启用、用户组是否有调用授权、模型是否被隐藏或自动停用，以及密钥是否绑定了已不可用的模型。

全站 API 关闭或密钥认证失败时会返回错误，不会用一个空数组表示成功。
