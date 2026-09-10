# 图像生成接口

`POST /v1/images/generations` 接受画面描述，返回 Base64 图片数据。它不会为 API 调用创建网页会话或附件链接。

## 请求示例

```bash
curl https://ai.example.com/v1/images/generations \
  -H "Authorization: Bearer $OBSIDIAN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "your-image-model",
    "prompt": "雨后街角的一家咖啡馆，水彩画。",
    "size": "1024x1024",
    "n": 1,
    "response_format": "b64_json"
  }'
```

模型必须已启用生图能力，且当前密钥有权访问。

## 参数

| 参数 | 当前行为 |
| --- | --- |
| `model` | 必填，指定可用的生图模型名称 |
| `prompt` | 必填，不能为空 |
| `n` | 默认 1；超过 4 时限制为 4 |
| `size` | 按上游支持填写，不保证所有尺寸可用 |
| `quality` | 传给上游，支持值由上游决定 |
| `style` | 传给上游，支持值由上游决定 |
| `response_format` | 当前网关固定向上游请求 `b64_json` |

`size` 和 `quality` 未填写时，网关没有统一补上 `1024x1024` 或 `standard`。最终行为由适配器与上游决定。

## 响应格式

```json
{
  "created": 1788998400,
  "data": [{
    "b64_json": "BASE64_IMAGE_DATA",
    "revised_prompt": "上游提供时才出现的修订提示词"
  }]
}
```

客户端解码 `b64_json` 并保存图片。示例中的 Base64 文字只是占位符，不能作为真实图片使用。

网关不透传上游签名 URL。如果上游忽略格式要求，只返回链接，接口会报格式错误，而不是返回永久图片地址。

## 用量与限制

图像请求同样执行账号权限、并发与额度检查，并写入用量记录。点数按模型配置和生图结算规则计算。

这个兼容端点不提供图片编辑、变体或参考图上传接口。网页图像工作台可以有不同的能力入口，不能直接套用到 `/v1/images/generations`。

参数不被上游接受时，先简化为单张生成，再核对该模型支持的尺寸和风格。
