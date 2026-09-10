# 常见问题

## 启动失败

### 端口已占用

典型报错：`bind: address already in use`
排查：

```bash
docker compose ps
lsof -i :8080
```

处理：

1. 修改 `OBSIDIAN_ADDR` 到空闲端口。
2. 调整 `docker-compose.yml` 的 `ports` 映射。

### PostgreSQL 模式下 DSN 未设置

典型报错：与 driver 相关的启动错误、数据库连接错误。
排查：

```bash
docker logs -f obsidian-arc
```

处理：

```bash
export OBSIDIAN_DB_DRIVER=postgresql
export OBSIDIAN_DB_DSN="postgres://user:pass@host:5432/dbname?sslmode=disable"
```

### Secret Key 不合法

典型报错：`secret key` 相关长度错误。
排查：

```bash
cat data/secret.key
```

处理：

`OBSIDIAN_SECRET_KEY` 为空时，服务会在 `DATA_DIR/secret.key` 自动生成并复用；如设置值必须长度至少 16。删除 `secret.key` 会导致已存储密钥无法解密，处理前请先备份 `/data`。

## 无法连接上游

### 上游返回连接错误

症状：`/api/chat` 返回 502/503，日志出现 `upstream`、`dial`、`timeout`。
排查步骤：

```bash
docker logs -f obsidian-arc
curl -I <provider-base-url>
```

处理：

1. 检查 `/admin/providers` 的 Base URL。
2. 检查出站网络（DNS/TLS/防火墙）。
3. 检查上游 API Key 是否和供应商侧一致。
4. 检查 `UPSTREAM_DIAL_TIMEOUT` 与 `UPSTREAM_HEADER_TIMEOUT`。

### 服务商可用性状态异常

症状：`/admin/dashboard` 或 `/api/admin/health` 显示上游不可用。
处理：

1. 检查服务商条目中 `kind` 与模型协议类型。
2. 在 `/admin/providers` 触发探测。

## 流式响应中断

### 客户端提前断开

症状：前端界面看到模型回复半截后停止。
排查日志：

```bash
docker logs -f obsidian-arc | grep -i stream
```

处理：

1. 检查浏览器是否刷新、切换网络、关闭代理加速。
2. 检查中间层超时（例如 Nginx 的 `proxy_read_timeout`）。
3. 调整请求端超时与上游代理配置。

### 服务端上下文提前结束

症状：后端日志有 `context canceled` 或 `request canceled`。
处理：

1. 检查前端是否在请求还未结束时关闭页面。
2. 检查代理是否对长连接有超时截断。
3. 检查是否将 `UPSTREAM_REQUEST_TIMEOUT` 设置为过小值。

## 关键日志入口

```text
启动日志：前端启动输出含 addr / db / version
运行日志：docker logs -f obsidian-arc
数据库健康：/api/health
数据库迁移问题：启动日志中的 migrate error
```
