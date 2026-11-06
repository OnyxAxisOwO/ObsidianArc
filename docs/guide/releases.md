# 版本发布

版本以 git 的带注释标签为准。正式版为 `vMAJOR.MINOR.PATCH`，候选版为 `vMAJOR.MINOR.PATCH-rc.N`；部署主分支不会自动成为正式版。

## 发布步骤

1. 在 `CHANGELOG.md` 中把 `Unreleased` 的内容归入新版本，填写发布日期，并在上方留下空的 `Unreleased`。
2. 将变更合并到 `main`，等该提交的 CI 全部通过，包括 Documentation、Gate 和 Image。
3. 在这个干净提交上创建带注释的标签，并推送：

   ```bash
   git switch main
   git pull --ff-only
   git status --short
   git tag -a vMAJOR.MINOR.PATCH -m vMAJOR.MINOR.PATCH
   git push origin vMAJOR.MINOR.PATCH
   ```

4. 在 [Actions](https://github.com/OnyxAxisOwO/ObsidianArc/actions/workflows/release.yml) 等待 `Release` 成功。工作流核对标签属于 `main`，且标签对应的提交已有成功的主分支 CI，然后构建并发布下列产物。
5. 核对 [GitHub Release](https://github.com/OnyxAxisOwO/ObsidianArc/releases)、镜像标签和下载文件。实例部署仍按该实例仓库的流程执行；正式部署从干净标签构建，服务报告的版本应与标签完全一致。

已推送的标签不能移动或重复使用。若标签先于 CI 通过而导致 Release 拒绝发布，等 CI 成功后重跑该工作流。

## 发布产物

| 产物 | 内容 |
| --- | --- |
| GitHub Release | 正文取自该标签的 `CHANGELOG.md` 版本段；候选版标记为预发布 |
| 二进制压缩包 | Linux、macOS、Windows，各含 amd64 和 arm64；Windows 为 `.zip`，其余为 `.tar.gz` |
| `SHA256SUMS` | 六个压缩包的 SHA256 校验值 |
| GHCR 镜像 | `ghcr.io/onyxaxisowo/obsidianarc:版本标签`，含 Linux amd64、arm64 的多架构清单 |
| `latest` | 最新正式版；候选版和旧版补发不会把它改成更早的版本 |

压缩包包含程序、`VERSION`、MIT 许可和对应版本的 README；前端已内嵌。镜像直接使用同一次构建的 Linux 程序，并在推送前启动 amd64 容器，检查健康响应中的版本。镜像另保留 `版本标签-amd64`、`版本标签-arm64`，可用于指定架构。

发布使用仓库自己的 `GITHUB_TOKEN`，不需要另设发布密钥。写权限只授予发布任务：`contents: write` 用于 Release，`packages: write` 用于 GHCR。

## 为已有标签补发

在主分支上执行：

```bash
gh workflow run release.yml --ref main -f tag=vMAJOR.MINOR.PATCH
gh run list --workflow release.yml
```

工作流取指定标签的源代码，重新生成产物并上传到已有 Release；不存在 Release 时，按该版本的更新日志创建。已有附件会被同名构建替换，标签和已发布正文保持不变。这个入口也用于重试中断的发布。

## 首次公开 GHCR 镜像

GitHub 新建的容器包默认是私有包。首次发布后，打开账户的 Packages → `obsidianarc` → Package settings → Change visibility，将它设为 Public。后续版本沿用包的可见性，无需重复设置。

公开后验证匿名拉取：

```bash
docker pull ghcr.io/onyxaxisowo/obsidianarc:版本标签
docker buildx imagetools inspect ghcr.io/onyxaxisowo/obsidianarc:版本标签
```

完整镜像是核心版本，不带任何实例仓库的编译插件。运行这些插件的实例继续使用自己的构建与部署流程，不能直接换成核心镜像。
