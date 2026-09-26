# 部署与升级

本页说明对外部署、升级和日常检查。首次安装见[安装](./introduction)，完整数据备份见[备份与迁移](../admin/backups-and-data)。

## 对外提供服务

程序本身提供 HTTP 服务。使用反向代理终止 HTTPS，并将程序监听地址限制在代理可访问的范围内。

对外部署时应配置：

| 环境变量 | 设置方式 |
| --- | --- |
| `OBSIDIAN_COOKIE_SECURE` | HTTPS 下保持 `true` |
| `OBSIDIAN_TRUST_PROXY` | 需要识别代理转发的客户端地址时设为 `true` |
| `OBSIDIAN_TRUSTED_PROXIES` | 填实际代理地址或尽可能小的网段 |
| 站点公开地址 | 在管理员后台「安全 → 邮件服务」填写 HTTPS 地址。邮件验证、第三方登录和对外的 OpenID 登录都用它；旧部署可继续用 `OBSIDIAN_PUBLIC_URL` 作回退值 |

没有固定公开地址时，部分对外链接会从请求本身推断地址，而这依赖整条链路都如实转发协议。
最常见的例外是代理前面还有一层代理、且那一层回源走明文——Cloudflare 的
Flexible 模式就是这样：协议一路变成 http，于是 issuer、回调地址和邮件链接
全都是 `http://`，对方会拒绝。用到这三样中的任何一样就把后台的公开地址填上。

代理需要支持长时间的流式响应。以 Nginx 的代理路径为例：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_read_timeout 600s;
}
```

这是代理路径片段，需放入已有的 HTTPS 站点配置中。上传较大附件时，还需要让代理的请求体限制与应用的附件上限相匹配。

## 使用 PostgreSQL

仓库根目录的 `docker-compose.yml` 使用 PostgreSQL，服务名为 `server` 和 `db`。在启动前设置固定的 `OBSIDIAN_SECRET_KEY`，并修改数据库密码。妥善保存这些值，后续重建必须继续使用。

```bash
export OBSIDIAN_SECRET_KEY='替换为固定保存的随机密钥，至少16个字符'
export POSTGRES_PASSWORD='替换为数据库密码'
docker compose up -d --build
```

该 Compose 配置默认按 HTTPS 反向代理部署，开启 Secure Cookie，只映射到本机地址。它还挂载了 Linux 的时区文件；在其他系统上使用前应调整这些挂载。

已有 PostgreSQL 可通过 `OBSIDIAN_DB_DRIVER=postgres` 和 `OBSIDIAN_DB_DSN` 接入。远程数据库的 TLS 参数由你的数据库环境决定。

切换驱动只会连接目标数据库并执行表结构迁移，**不会搬迁 SQLite 中的已有数据**。

## 升级 Docker 实例

先完成备份，并留存当前可运行版本。下面对应[安装页](./introduction)的本机 Docker 示例，在仓库根目录执行：

```bash
git pull --ff-only
docker build -t obsidian-arc:latest .
docker stop obsidian-arc
docker rm obsidian-arc
docker run -d --name obsidian-arc \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e OBSIDIAN_COOKIE_SECURE=false \
  -v obsidian-arc-data:/data \
  obsidian-arc:latest
```

对外部署时沿用原有的 HTTPS、密钥和代理配置，不要直接套用本机示例。镜像由源码本地构建，不需要从镜像仓库执行 `docker pull`。

使用仓库的 PostgreSQL Compose 配置时：

```bash
git pull --ff-only
docker compose up -d --build server
docker compose logs --tail 100 server
```

### 在本地构建后部署

服务器配置较低时，上面的 `--build` 会在服务器上完整执行前端打包和 Go 编译，耗时较长。程序是不依赖 C 库的静态二进制，可以在任何装有 Go 和 Node.js 的电脑上为 Linux 交叉编译，服务器只需把现成的程序装进镜像：

```bash
make deploy DEPLOY_HOST=root@你的服务器
```

这条命令会：

1. 在本机打包前端，并编译 Linux 程序，输出到 `dist/`（也可以单独执行 `make release`）；
2. 通过一条 SSH 连接把 `dist/` 上传到服务器的 `/data/obsidian-arc/dist/`；
3. 在服务器上用 `Dockerfile.release` 生成镜像（只复制程序，几秒完成），标记为 `obsidian-arc:latest` 和本次版本号；
4. 执行 `docker compose up -d --no-build server`，只替换 `server` 容器，数据库容器不受影响。

默认编译 x86-64 程序。服务器是 ARM 时加 `ARCH=arm64`；架构不一致时，命令会在替换任何东西之前停止并提示。Compose 项目不在 `/data/obsidian-arc` 时，用 `DEPLOY_DIR=/路径` 指定。服务器上的 `docker-compose.yml` 需要保留 `image: obsidian-arc:latest`，仓库自带的配置已经是这样。

每次部署的镜像都会额外保留版本号标签。需要回退时，在服务器的 Compose 目录执行：

```bash
docker image ls obsidian-arc
docker tag obsidian-arc:上一个版本号 obsidian-arc:latest
docker compose up -d --no-build server
```

旧版本镜像不会自动删除，可以定期用 `docker image rm obsidian-arc:版本号` 清理。

## 升级二进制实例

重新构建前端与程序，停止旧进程，再替换二进制文件。保持原有数据目录、环境变量和服务运行用户不变。

程序启动时自动执行未应用的数据库迁移。回滚程序不等于回滚数据库；升级后发生问题时，应根据迁移情况决定是否恢复升级前的备份。

## 日常检查

```bash
curl http://localhost:8080/api/health
docker logs --tail 100 obsidian-arc
```

Compose 部署使用 `docker compose logs --tail 100 server`。通过 systemd 托管时，使用你实际配置的服务名查看日志。

健康接口检查程序响应，后台的可用性页面检查模型调用。两者用途不同，健康接口正常并不代表上游模型可用。
