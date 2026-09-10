---
layout: home

hero:
  name: Obsidian Arc
  text: 自托管多用户 AI 对话服务与 API 网关
  tagline: Go 单二进制 + SQLite/PostgreSQL
  actions:
    - theme: brand
      text: 安装
      link: /guide/introduction
    - theme: alt
      text: GitHub 仓库
      link: https://github.com/OnyxAxisOwO/ObsidianArc

features:
  - title: 单进程单二进制
    details: Go 1.22 原生构建，静态前端资源编译嵌入，内置 SQLite 与 PostgreSQL 双驱动支持，无额外中间件依赖。
    icon: ⚡
  - title: 全协议兼容网关
    details: 统一承载 OpenAI Completions、Anthropic Messages (Claude Code) 与 Codex 接口，支持服务商调度与思维链解析。
    icon: 🌐
  - title: 严格并发与风控
    details: 数据库行级锁并发约束、无状态 API 密钥鉴权、不可变审计日志与请求生命周期级取消控制。
    icon: 🛡️
---

## 最短可用路径

```bash
git clone https://github.com/OnyxAxisOwO/ObsidianArc.git
cd ObsidianArc
make docker VERSION=v2026.09.10.00.00.00
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
    volumes:
      - ./data:/data
EOF
docker compose up -d
```

打开 `http://localhost:8080`。首次启动未设置管理员环境变量时打开 `/register` 注册首个账号并登录；若设置了 `OBSIDIAN_ADMIN_USER` 与 `OBSIDIAN_ADMIN_PASSWORD`，则打开 `/login` 用该管理员账号登录。

## 文档目录

- [安装](/guide/introduction)
- [首次配置](/guide/quick-start)
- [配置参考](/guide/configuration)
- [运维](/guide/deployment)
- [常见问题](/guide/troubleshooting)
