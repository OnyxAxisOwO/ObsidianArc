# 安装

Obsidian Arc 是一个自托管的 AI 工作空间和 API 网关。前端随 Go 程序一起打包，默认使用 SQLite；配置好上游服务商与模型后，就可以在浏览器中对话。

## 使用 Docker

需要安装 Docker。正式版镜像支持 Linux amd64 和 arm64，Docker 会自动选择当前架构：

```bash
docker pull ghcr.io/onyxaxisowo/obsidianarc:latest
docker volume create obsidian-arc-data
docker run -d --name obsidian-arc \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e OBSIDIAN_COOKIE_SECURE=false \
  -v obsidian-arc-data:/data \
  ghcr.io/onyxaxisowo/obsidianarc:latest
```

打开 `http://localhost:8080`。这个示例供本机通过 HTTP 使用，因此只绑定本机地址，并关闭 Secure Cookie。对外提供服务时，先配置 HTTPS，再启用 Secure Cookie，见[部署与升级](./deployment)。

数据卷保存数据库和自动生成的实例密钥。重新创建容器时继续挂载这个卷，已有数据就会保留。

`latest` 指向最新正式版。需要固定版本时，换成对应的 `vMAJOR.MINOR.PATCH` 标签；候选版使用 `vMAJOR.MINOR.PATCH-rc.N`，不会覆盖 `latest`。

## 下载二进制

[GitHub Releases](https://github.com/OnyxAxisOwO/ObsidianArc/releases) 提供 Linux、macOS、Windows 的 amd64 和 arm64 压缩包，前端已内嵌，无需安装 Go 或 Node.js。下载匹配操作系统与架构的文件，解压后运行 `obsidian-arc`（Windows 为 `obsidian-arc.exe`）。

每个版本附带 `SHA256SUMS`；在下载目录执行 `sha256sum --ignore-missing -c SHA256SUMS` 可核对已有文件。macOS 可用 `shasum -a 256 文件名` 与清单比对，Windows PowerShell 可用 `Get-FileHash 文件名 -Algorithm SHA256`。

## 从源码构建

需要 Git、Node.js 22、npm，以及满足根目录 `go.mod` 要求的 Go 工具链。当前仓库声明 Go 1.27.0；构建工具版本以仓库文件为准。

先构建前端，再编译 Go 程序。只执行 `go build` 不会生成前端资源。

```bash
git clone https://github.com/OnyxAxisOwO/ObsidianArc.git
cd ObsidianArc
npm --prefix web ci --no-fund --no-audit
npm --prefix web run build
go build -trimpath -o bin/obsidian-arc ./cmd/server
OBSIDIAN_ADDR=127.0.0.1:8080 OBSIDIAN_COOKIE_SECURE=false ./bin/obsidian-arc
```

Windows PowerShell 的最后两步改为：

```powershell
go build -trimpath -o bin/obsidian-arc.exe ./cmd/server
$env:OBSIDIAN_COOKIE_SECURE = "false"
$env:OBSIDIAN_ADDR = "127.0.0.1:8080"
./bin/obsidian-arc.exe
```

上面的本机示例把监听地址限制为 `127.0.0.1:8080`。未设置地址时，程序默认监听 `:8080`。数据写到工作目录下的 `data` 文件夹，需要固定位置时设置 `OBSIDIAN_DATA_DIR`。

## 创建管理员

没有默认账号或默认密码。空数据库首次启动时：

- 未提供初始化账号：打开 `/register`，首个注册用户成为管理员。
- 同时提供 `OBSIDIAN_ADMIN_USER` 与 `OBSIDIAN_ADMIN_PASSWORD`：程序创建该管理员，随后到 `/login` 登录。

初始化账号变量只在用户表为空时生效，不能用来重置已有账号的密码。首次配置完成前，应限制其他人访问注册入口。

## 检查是否启动成功

```bash
curl http://localhost:8080/api/health
```

健康接口可用于检查程序是否响应；模型能否生成回答，需要另外配置并测试上游。

下一步：[首次配置](./quick-start)。
