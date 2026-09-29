# 插件

Obsidian Arc 的核心是一个通用的 AI 对话站点。只有部分实例才需要的功能——比如围绕某个 QQ 群运营的站点要用的 QQ 号、退群处理，或者某个运营方自建的风控服务——以**插件**的形式存在：放在仓库的 `plugins/` 目录下，按需编译进二进制。

没编译进去的插件不贡献任何代码、路由、数据表或界面文字；二进制仍然是部署说明里承诺的那一个文件。

## 当前的插件

| 插件 | 作用 | 带来的设置 |
| --- | --- | --- |
| `qqgroup` | 账户的 QQ 号字段（注册要求、唯一、可搜索）；OIDC 主体号即 QQ 号时自动绑定；退群处理（后台操作与机器人 Webhook）、退群记录、邀请人列表里的退群标记 | `registration.qq_requirement`、`bot.webhook_token`、`bot.departure_mode` |
| `riskcontrol` | 超级风控：注册（验证码模式选「仅超级风控」时）与登录前的令牌校验 | `risk.base_url`、`risk.site`、`risk.secret_key`、`risk.on_login` |

设置键、数据表和迁移版本号都沿用它们还在核心里时的名字，所以已经在用这些功能的实例只要继续把插件编译进去，什么都不用迁移。

## 选择编译哪些插件

```bash
make build                          # 默认：PLUGINS="qqgroup riskcontrol"
make build PLUGINS=                 # 只要核心
make deploy DEPLOY_HOST=… PLUGINS=qqgroup
go build ./cmd/server               # 不带任何构建标签 = 只要核心
```

每个插件对应一个构建标签 `plugin_<名字>`，由 `cmd/server/plugin_<名字>.go` 引入。Docker 镜像同样接受 `--build-arg PLUGINS="…"`，默认值和 Makefile 一致。

`make version` 会顺带打印这次构建带了哪些插件，`make release` 也会把它写进 `dist/PLUGINS`。

## 去掉一个插件会怎样

- 它的路由不再存在（返回 404），它的设置键会被后台当作未知键拒绝写入；
- 数据库里它留下的数据**原样保留**：列还在、表还在，核心读写时绕开它们；
- 它留下的设置行不会出现在后台设置接口里——那可能是它的密钥，而负责给密钥打码的正是这个已经不在的插件；
- 以后重新编译进来，它接着用原来的数据。

## 插件能改动什么

插件只能通过核心模块显式开放的扩展点接入，方向永远是插件依赖核心、核心不知道任何插件：

| 扩展点 | 用途 |
| --- | --- |
| `database.Migrate` 的插件迁移目录 | 插件自己的表和列。从核心搬出来的迁移保留原版本号；插件新写的迁移命名为 `<插件>_NNNN_*.sql` |
| `settings.Define` / `settings.AddCaptchaMode` | 插件的设置项（默认值、是否为密钥、所属后台权限、校验）和注册验证码模式 |
| `user.DefineField` + `auth.Service.SetFieldRule` | 账户上的附加字段：自动参与查询、扫描、写入、唯一性检查与后台搜索；注册时是否必填由插件的设置决定 |
| `auth.Service.AddGuard` | 注册 / 登录前的检查，令牌放在请求体的 `guards` 里 |
| `auth.Handlers.Extend` | `/api/site` 里 `plugins.<名字>` 下给浏览器的配置 |
| `oauth.Service.BindSubject` | 某个登录提供方的主体号即某个账户字段的值 |
| `admin.Handlers.Mount` / `console.Register` | 后台接口（和其他后台接口走同一层权限包装）与控制台命令 |
| `invite.Handlers.DecorateInvitees` | 给邀请人自己的邀请列表补充信息 |
| `plugin.Host.AllowOrigin` | 页面需要加载第三方服务脚本时放宽 CSP 的来源 |

## 浏览器端

每个插件在 `web/src/plugins/<名字>/<名字>.plugin.ts` 有一个对应模块，构建成一个独立的小 chunk（几 KB），只有当 `/api/site` 列出了这个插件时才会下载。

插件模块只做**声明**，不写界面：账户字段长什么样、守卫怎么拿令牌、设置卡片里有哪些控件、一张分页列表、账户面板里的几个按钮、错误码和通知的文案、它自己的中英文字典。核心用自己的组件把这些画出来，所以插件的设置会和同页其他设置一起标记未保存、一起提交、一起报错，也不会带来一套风格不一致的控件。
