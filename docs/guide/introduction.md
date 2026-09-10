# 安装

## Docker Compose

```bash
git clone https://github.com/OnyxAxisOwO/ObsidianArc.git
cd ObsidianArc
mkdir -p data
cat > docker-compose.yml <<'EOF'
services:
  obsidian-arc:
    image: obsidian-arc:latest
    container_name: obsidian-arc
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      OBSIDIAN_ADDR: ":8080"
      OBSIDIAN_DATA_DIR: "/data"
      OBSIDIAN_DB_DRIVER: "sqlite"
    volumes:
      - ./data:/data
EOF
docker compose up -d
```

```bash
docker compose logs -f obsidian-arc
```

启动成功后访问 `http://localhost:8080`。

## 二进制启动

```bash
git clone https://github.com/OnyxAxisOwO/ObsidianArc.git
cd ObsidianArc
go build -o obsidian-arc ./cmd/server
./obsidian-arc
```

启动成功后访问 `http://localhost:8080`。

## 镜像名、构建参数、挂载路径（代码确认）

`Dockerfile` 最终镜像基于 `gcr.io/distroless/static-debian12:nonroot`。
`Makefile docker` 目标命令为：

```bash
docker build --build-arg VERSION=<value> -t obsidian-arc:<VERSION> -t obsidian-arc:latest .
```

运行时固定监听 `/data`，在镜像里设置了 `VOLUME /data` 与 `WORKDIR /data`。
容器端口映射默认到 `8080`。

## 最少需要设置的环境变量

```text
sqlite 模式：不设置也可启动（使用默认值）
postgresql 模式：必须设置 OBSIDIAN_DB_DRIVER=postgresql 和 OBSIDIAN_DB_DSN
```

## 端口与路径

`OBSIDIAN_ADDR` 默认 `:8080`。
服务端固定注册 `/v1` 兼容网关前缀和 `/api` 业务前缀，`/` 为前端入口与 SPA 回退。

## 首次启动数据库行为

```text
1) 服务启动调用迁移器，执行 migrations 表未执行的 SQL 文件
2) 自动补齐默认分组 Default（allow_all_models=true）
3) 若 users 为空且设置了 ADMIN_USER 与 ADMIN_PASSWORD 则创建管理员
4) 若未设置 ADMIN_*，不会自动创建管理员，首个注册用户会在注册流程里被设为管理员
```

```text
ADMIN_USER/ADMIN_PASSWORD/ADMIN_EMAIL 默认都为空，不存在默认账号和默认密码
```

## 默认端口和路由清单（代码确认）

```text
监听端口：:8080（可改 OBSIDIAN_ADDR）

V1 前缀：
/v1/chat/completions
/v1/messages
/v1/messages/count_tokens
/v1/responses
/v1/images/generations
/v1/models
/v1/models/{id}

API 前缀：
/api/health
/api/site
/api/auth/*
/api/profile
/api/profile/name
/api/profile/email
/api/profile/password
/api/preferences
/api/models
/api/chat
/api/trial/chat
/api/conversations*
/api/attachments*
/api/usage/me
/api/usage/me/history
/api/usage/cards*
/api/keys*
/api/account/export
/api/account/import
/api/announcements*
/api/images/generate
/api/admin/*
/api/uptime
```

路由星号 `*` 表示该前缀下有多个子路径，具体路径见后续页面。
