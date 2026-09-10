# 首次配置

## 0. 登录入口

```text
首次启动未设置 ADMIN_* 时：
打开 /register，创建首个账号并登录

设置了 ADMIN_USER/ADMIN_PASSWORD 时：
打开 /login，使用管理员账号登录
```

## 1. 添加上游服务商

1. 登录后打开 `/admin` 进入后台。
2. 在左侧菜单点 `供应商`（页面地址 `/admin/providers`）。
3. 点击新增，填入服务商名称、类型、Base URL、API Key。
4. 保存后在列表里确认显示为可用。
5. 可回到服务商列表点击测试连接，确认返回成功。

## 2. 添加模型

1. 在左侧菜单点 `模型`（页面地址 `/admin/models`）。
2. 点击新增模型。
3. 选择刚才创建的供应商。
4. 填写模型标识（上游模型 ID）和展示名称。
5. 保存后用默认权限测试一次发起对话。

## 3. 创建普通用户

1. 仍在管理员后台左侧菜单点 `用户`（页面地址 `/admin/users`）。
2. 点击新增用户。
3. 设置用户名、邮箱、初始密码、分组。
4. 保存后把角色保持为 `user`（非管理员）。
5. 如果你只想先用管理员账号验证，跳过此步不影响系统运行。

## 4. 配置普通用户 API Key

1. 以普通用户登录进入前端后打开 `/keys`。
2. 点击新增 Key。
3. 复制返回的明文 Key 作为客户端调用凭证。
4. 把 Key 绑定到你在 `/admin/models` 列表里启用的模型上。

## 5. 直接调用验证

### 健康检查

```bash
curl http://localhost:8080/api/health
```

### 测试 chat 请求

```bash
curl -H "Authorization: Bearer <KEY>" \
  -H "Content-Type: application/json" \
  -d '{"model_id":"<MODEL_ID>","messages":[{"role":"user","content":"ping"}]}' \
  http://localhost:8080/api/chat
```

### 测试兼容接口（可选）

```bash
curl http://localhost:8080/v1/models -H "Authorization: Bearer <KEY>"
```

## 首次配置顺序（可复制）

```text
启动服务
↓
注册首个管理员或管理员登录
↓
/admin/providers 新增服务商
↓
/admin/models 新增模型
↓
/admin/users 新增普通用户
↓
/keys 创建普通用户 API Key
```
