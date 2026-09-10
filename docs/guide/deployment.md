# 运维

## 升级

### Docker Compose 升级（SQLite）

```bash
git -C ObsidianArc pull
make docker VERSION=v$(date +%Y.%m.%d.%H.%M.%S)
docker compose pull obsidian-arc || true
docker compose up -d --pull always
docker compose ps
```

### 二进制升级

```bash
git -C ObsidianArc pull
cd ObsidianArc
go build -o obsidian-arc ./cmd/server
cp obsidian-arc /usr/local/bin/obsidian-arc
systemctl restart obsidian-arc
```

## 数据备份

### SQLite（默认）

```bash
docker cp obsidian-arc:/data ./backup-$(date +%F)
```

或者使用绑定目录时直接备份：

```bash
tar -czf obsidian-arc-data-$(date +%F).tar.gz data
```

### PostgreSQL

```bash
docker compose exec postgres pg_dump -U arc arc > obsidian-arc-pg-$(date +%F).sql
```

### 恢复

```bash
tar -xzf obsidian-arc-data-YYYY-MM-DD.tar.gz
docker cp ./backup-YYYY-MM-DD obsidian-arc:/data
```

## 日志

```bash
docker logs -f obsidian-arc
```

二进制运行时日志在当前终端可直接看到。
如果通过 systemd 运行，日志路径见系统日志：

```bash
journalctl -u obsidian-arc -f
```

## 改端口

```bash
export OBSIDIAN_ADDR=":18080"
docker compose down
docker compose up -d
```

或者编辑 `docker-compose.yml` 中 `OBSIDIAN_ADDR` 和 `ports`，例如 `18080:18080`。

## 切换到 PostgreSQL

```text
1) 准备 PostgreSQL 连接串并能连通
2) 停止服务
3) 配置 OBSIDIAN_DB_DRIVER=postgresql
4) 配置 OBSIDIAN_DB_DSN（标准 DSN）
5) 重启服务
```

```bash
cat > docker-compose.yml <<'EOF'
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: arc
      POSTGRES_PASSWORD: arc-pass
      POSTGRES_DB: arc
    volumes:
      - pg-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U arc -d arc"]
      interval: 5s
      timeout: 5s
      retries: 5
  obsidian-arc:
    image: obsidian-arc:latest
    environment:
      OBSIDIAN_ADDR: ":8080"
      OBSIDIAN_DB_DRIVER: "postgresql"
      OBSIDIAN_DB_DSN: "postgres://arc:arc-pass@postgres:5432/arc?sslmode=disable"
      OBSIDIAN_DATA_DIR: "/data"
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
volumes:
  pg-data:
EOF
docker compose up -d
```

启动后迁移仍会在服务启动时自动执行，表结构由 `migrations` 驱动。
