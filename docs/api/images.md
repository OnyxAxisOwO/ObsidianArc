# 图像生成接口

`POST /v1/images/generations` 端点遵循 OpenAI 图像生成 API 规范，支持文生图、分辨率指定与画质选择。

---

## 接口规范

- **请求方法**：`POST`
- **请求地址**：`https://<你的域名>/v1/images/generations`
- **认证方式**：HTTP Header `Authorization: Bearer sk-oa-<你的APIKey>`

### 请求体参数

| 参数名 | 类型 | 必填 | 默认值 | 说明 |
| :--- | :--- | :--- | :--- | :--- |
| `prompt` | string | **是** | — | 图像生成的文本描述。 |
| `model` | string | 否 | 系统默认 | 生图模型名称（如 `dall-e-3`、`flux-dev`）。 |
| `n` | int | 否 | `1` | 生成图片张数（系统单次上限为 4 张）。 |
| `size` | string | 否 | `1024x1024` | 画面分辨率（如 `1024x1024`、`1792x1024`、`1024x1792`）。 |
| `quality` | string | 否 | `standard` | 画质模式（`standard` 或 `hd`）。 |
| `response_format` | string | 否 | `url` | 返回格式（`url` 或 `b64_json`）。 |

---

## 图片转存与生命周期管理

### 做了什么
当上游生图模型生成图像并返回外部临时 URL 时，网关在将响应返回给客户端前，在服务端通过安全过滤器将图片下载至本地数据目录（`/data/uploads/`），并返回实例自身的持久化访问路径（`/api/attachments/...`）。

### 为什么这么做
上游服务商（如 OpenAI DALL-E）返回的临时 CDN 链接通常在 1~2 小时后过期。通过服务端本地转存，使调用方获取到的图片 URL 长期有效。

### 代价是什么
API 调用的总体耗时增加了服务端下载图片所需的时间（受上游 CDN 带宽影响）；同时会占用宿主机的磁盘存储空间。

---

## 代码调用示例

### 1. cURL 命令行调用

```bash
curl https://ai.example.com/v1/images/generations \
  -H "Authorization: Bearer sk-oa-xxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "dall-e-3",
    "prompt": "秋天雨夜中的街角咖啡馆，暖黄色灯光，水彩画风格",
    "size": "1024x1024",
    "n": 1
  }'
```

### 2. Python SDK 调用

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://ai.example.com/v1",
    api_key="sk-oa-xxxxxxxxxxxx",
)

response = client.images.generate(
    model="dall-e-3",
    prompt="A quiet coffee shop in autumn evening, warm lights, watercolor style",
    size="1024x1024",
    quality="standard",
    n=1,
)

image_url = response.data[0].url
print("生成的图片地址：", image_url)
```

---

## 运维约束与排查路径

### 失效场景
1. **上游服务商不支持请求的分辨率**：不同模型支持的分辨率集合不同（例如 DALL-E 2 不支持 1792x1024），若传入不受支持的尺寸，上游会直接返回 400 校验错误；
2. **生成张数超过系统单次限制**：若 `n > 4`，网关会直接返回 400 错误，拦截超量并发生成；
3. **磁盘空间耗尽导致转存失败**：若服务端附件目录空间不足，本地转存失败会使整个生图请求返回 500 错误。

### 不明显的约束与前提
1. **单次生成硬上限**：系统对单次生图请求的生成张数限制为最多 4 张（`MaxImagesPerRequest = 4`），超出将被前置拦截；
2. **防 SSRF 转存过滤网段**：图片下载器仅放行公网合法 IP 地址，如果上游返回的图片重定向到内网私有地址（如 `10.0.0.0/8` 或 `127.0.0.1`），转存将被安全过滤器拦截；
3. **图片生命周期与存储依赖**：转存后的图片物理保存在 `./data/uploads/` 目录中，在容器化部署时必须确保该目录挂载了持久化数据卷。

### 排查步骤
1. **核对错误码**：在管理后台「请求日志」中找到生图记录，确认是参数校验未通过还是上游额度耗尽；
2. **检查宿主机磁盘**：进入管理后台「系统资源」核对磁盘已用空间。
