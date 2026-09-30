# 插件包

插件包是一个 `.arcx` 文件：一个 zip，里面有清单（`manifest.json`）、编译成 WebAssembly 的后端、浏览器端的一个 JS 模块，以及建表和删表的 SQL。把它拖进后台「插件」页就装好了；可以启用、禁用、删除；删除是真删——它从列表里消失，存进数据库的包本体被删掉，不需要重启，也不需要重新编译。

这是[编译进二进制的插件](./plugins)之外的另一条路。两种插件用的是同一组扩展点（设置、账户字段、注册与登录守卫、路由、控制台命令……），后台和其余代码分不出区别；区别只在代码从哪来：一种编进二进制、随构建而定，一种是数据、随时装卸。

## 装一个包会发生什么

1. **上传即预览。** 文件拖进页面，服务器只读它：检查 zip、检查清单、检查它定义的东西会不会和服务器已有的撞车。**什么都没有存，什么都没有跑。** 对话框列出它是谁、哪个版本、多大、SHA-256、它要哪些权限、有没有在页面里运行的 JS。
2. **确认才安装。** 操作员看过权限、填好初始设置、选择是否立即启用，才点安装。在开启了「插件操作需要两步验证码」的实例上还要输入验证码。
3. **一个事务做完全部。** 迁移、初始设置、包本体、状态、挂到运行中服务器的各个扩展点，一起成功或一起回滚。它的迁移在 SQLite 和 PostgreSQL 上都要能跑（和核心的迁移同一条规则，安装时就检查）。
4. **更新是原地的。** 上传同名的新版本，对话框写「更新 1.0.0 → 1.1.0」，状态和设置原样保留，变的只有代码。守卫在替换的一瞬间也不会出现敞口。
5. **删除。** 默认保留它的数据（表、列、设置），以后重装接着用；勾选「同时删除数据」则执行包里的 `purge/` 脚本、删掉它的设置行、忘记它的迁移，重装从空表开始。两种都会让它从列表里消失。

## 包的结构

```
manifest.json          必须
plugin.wasm            后端（清单里 backend 指向它，可选）
web/ui.js              浏览器端（清单里 ui.module 指向它，可选），以及它用到的其他静态文件
migrations/*.sql       建表、加列，按文件名顺序执行，各记一条 schema_migrations
purge/*.sql            「同时删除数据」时执行的清理，按文件名顺序
README.md, LICENSE     随便放，不读
```

zip 里只允许上面这些；路径里出现 `..`、绝对路径、反斜杠、符号链接、隐藏文件都会被拒绝。压缩包不超过 32 MiB，解压后合计不超过 96 MiB，条目不超过 400 个。**包永远不会被解压到磁盘上**：安装就是把字节存进数据库的 `plugin_packages` 表，所以备份带得走它，第二个实例也读得到它。

## manifest.json

严格解析：不认识的字段是错误。下面每项都是可选的，除非标了「必须」。

| 字段 | 含义 |
| --- | --- |
| `arcx` | 必须，`1` |
| `name` | 必须，2–32 位小写字母和数字，字母开头。它是设置的所有者、路由的命名空间、迁移文件名的前缀 |
| `version` | 必须，`1.2.0` 这样的版本 |
| `title`, `description` | 必须有 `title.en`；`{ "en": "...", "zh": "..." }` |
| `author`, `homepage`, `license` | 显示用 |
| `requires.api` | 必须，插件接口的版本，目前是 `1`；服务器提供的更低则拒绝安装 |
| `permissions` | 后端要用的权限，见下 |
| `backend` | 后端文件，`plugin.wasm` |
| `hooks` | `describe`（后端告诉浏览器和页面策略「现在该说什么」）、`decorate_invitees`（给邀请人自己的邀请列表加内容） |
| `settings` | 插件的设置：`key`（`risk.base_url` 这样带点的小写名）、`default`、`secret`（只写，不回显）、`permission`（哪个后台权限可读写，如 `security`）、`enum` 或 `pattern`（校验），以及安装对话框用的 `label`、`hint`、`initial`（安装时询问） |
| `captcha_modes` | 往「注册验证码模式」的下拉里加的选项 |
| `fields` | 账户上的字段：`key`、`unique`、`searchable`、`pattern`。**列本身由插件自己的迁移创建** |
| `field_rules` | 哪个设置决定注册时这个字段是 `off`、`optional` 还是 `required` |
| `oauth_bindings` | OIDC 登录的主体号形如 `pattern` 时，就是这个字段的值（自动绑定，且该连接不可解绑） |
| `guards` | 注册或登录前的检查：`action`（`register`/`login`）、`name`、`event`（拒绝时写进安全日志的事件名） |
| `routes` | 后端提供的接口：`pattern`（`POST /api/admin/x/demo/things/{id}`）、`access`（`admin` 走后台的全部检查：会话、两步验证、`permission` 权限；`public` 原样开放，后端自己鉴权） |
| `console` | 控制台命令：名字、分组、摘要、用法、帮助、参数、标志、权限、是否破坏性 |
| `ui.module` | 浏览器端模块，`web/ui.js` |

冲突在安装前就查：设置键、账户列、验证码模式、控制台命令名、路由，只要与核心或另一个插件撞了，预览就直接报错，不动任何东西。

路由的约定：管理接口放在 `/api/admin/x/<插件名>/…`，公开接口放在 `/api/x/<插件名>/…`。需要沿用外部已经配置的地址（比如已经填进机器人里的 webhook）时，公开路由可以是 `/api/` 下任何核心没有占用的路径。

## 权限

后端能向服务器要的东西分成几族；清单里没声明的，调用一律返回 `permission_denied`。

| 权限 | 能做什么 | 主机调用 |
| --- | --- | --- |
| `db` | 对实例数据库执行任意 SQL（用 `?` 占位，两种数据库通用）、开事务 | `db.query`、`db.exec`、`db.begin`、`db.commit`、`db.rollback` |
| `network` | 向服务器能到达的任意地址发 HTTP(S) 请求（禁止链路本地地址）；**开着事务时不允许** | `http.fetch` |
| `users` | 挂起、恢复、删除账户，数活跃管理员——按后台自己的规则做 | `users.set_status`、`users.delete`、`users.count_active_admins` |
| `cards` | 收回账户名下未用的重置卡 | `cards.revoke_available` |
| `sessions` | 结束账户的所有登录 | `sessions.revoke_user` |
| `notify` | 往账户的收件箱放通知 | `notify.push` |
| `security_log` | 写安全日志 | `security.record` |
| `console` | 以当前操作员身份调用后台接口 | `console.call`、`console.resolve_user` |

不需要权限的：写日志、生成 ULID、读**自己的**设置（和 `registration.captcha_mode`、`site.name` 两个核心设置）、读配置的公开地址。

`db` 权限等于给了插件和核心自己的代码一样的手：账户、卡、邀请，全在里面，并且表结构不是稳定接口。安装对话框会把它标成高风险。

一次调用的限额：15 秒、64 MiB 内存、请求和响应各 8 MiB；一次调用里最多一个事务，调用结束时没提交的事务会被回滚。

## 后端

后端是一个 Go 程序，用 SDK（`sdk/arc`）写，编译成 `wasip1` 的 reactor：

```go
package main

import "github.com/OnyxAxisOwO/ObsidianArc/sdk/arc"

func main() {}

func init() {
	arc.Guard("demo", func(c *arc.Ctx, req arc.GuardRequest) (arc.GuardResult, error) {
		if req.Token == "block" {
			return arc.GuardResult{}, &arc.Refusal{Status: 403, Code: "demo_blocked", Message: "…", Reason: "blocked"}
		}
		return arc.GuardResult{}, nil
	})
	arc.Route("POST /api/admin/x/demo/things", func(c *arc.Ctx, r *arc.Request) (*arc.Response, error) {
		return arc.JSON(201, map[string]string{"ok": "yes"})
	})
}
```

```bash
go run ./cmd/arcpack build path/to/plugin -o demo.arcx     # 编译后端、打包
go run ./cmd/arcpack inspect demo.arcx                      # 看它要什么
```

**每次调用是一个全新的模块实例**（约 2.5 毫秒），互不相通，没有跨调用的内存。状态放在数据库、设置里。`time.Now()` 是服务器的真实时间，`crypto/rand` 是真随机数（wazero 默认给的是假时钟和确定的随机源，运行时已经替换掉了）；没有文件系统、没有环境变量。SDK 里一次调用的全部能力都是 `*arc.Ctx` 的方法：`Query`/`QueryRow`/`Exec`/`Tx`、`Fetch`、`Setting`、`NewID`、`Log`、`RevokeSessions`、`Notify`、`RecordSecurity`、`SetStatus`、`DeleteUser`、`CountActiveAdmins`、`RevokeCards`。`Tx` 之内的调用自动并入事务。

- **守卫**：注册或登录前被调用。返回 `*arc.Refusal` 拒绝（自己定状态码、错误码、话），返回 `GuardResult{Restrict: true}` 放行但让新账户的 API 保持关闭；后端崩了或超时则按拒绝处理——检查挂了，门不能开着。
- **路由**：拿到已经过会话和权限检查的请求。返回 `*arc.Error` 是你想给客户端看的错误；其他错误是 500，原因进日志、不给客户端。
- **`OnDescribe`**：告诉服务器「浏览器该看到的 `/api/site` 区块」和「页面的 Content-Security-Policy 现在该信任哪些来源」。**只在插件启用时和它自己的设置变化时被问，不是每个请求**——所以只能从设置算，不能读会变的东西。
- **`OnDecorateInvitees`**：拿到邀请人自己的邀请列表，返回整张列表；可以改、可以加（比如加上已删除账户的记录）。
- **控制台命令**：`arc.Command("demo things", …)`，用 `Console.Call` 去调后台接口，`Table`、`Printf` 出结果。接口拒绝了命令要做的事时，把 `Call` 的错误原样返回即可，控制台显示的是接口自己的那句话，而不是「插件崩溃」。

## 浏览器端

`web/ui.js` 是一个 ES 模块，默认导出一个函数，接收页面借给插件的东西，返回插件的声明：

```js
export default function (host) {
  // host.api        get / post / put / patch / delete，以及 ApiError
  // host.icons      图标组件，如 host.icons.IconUsers
  // host.strings    双语词典 helper：host.strings({ hello: 'Hello' }, { hello: '你好' })
  // host.format     absoluteTime(ms)
  return { name: 'demo', fields: {…}, settings: […], lists: […], adminPages: […] };
}
```

声明的形状是 `web/src/plugins/types.ts` 里的 `ArcPlugin`：账户字段、守卫（怎么拿令牌）、设置卡片、列表、操作卡片、账户面板里的按钮、错误码和通知的文案、自己的后台子页面。核心用自己的组件把它们画出来，插件不带自己的样式。

页面通过 `/api/site` 里这个插件的区块里的 `_ui` 知道去哪取模块（`/api/x/<name>/web/ui.js?v=<hash>`），**只有插件启用时才取**；地址带版本哈希，可以永久缓存；模块被当作同源脚本动态导入。**它在页面里以页面的权限运行**——能调用登录用户能调用的一切接口——所以清单里有 `ui` 的包，安装对话框会明确写出来。

## 随部署自带的包

配置 `OBSIDIAN_PLUGIN_DIR`（镜像里默认 `/usr/local/share/obsidian-arc/plugins`）指向一个放着 `.arcx` 的目录，服务器启动时在处理第一个请求之前：

- 如果实例已有这个插件的状态记录（它以前是编进二进制的插件，状态是启用或禁用）而还没有包：**接管**——状态、设置、数据原样保留，从第一个请求起就以包的形式运行；
- 如果之前是从这个目录装的、现在目录里的版本不更旧：**更新**；
- 如果是操作员自己上传的：**不动**；如果是操作员删掉的：**不再装回来**；
- 全新的：装上，**默认禁用**，等操作员看过再启用。

## 与编译进二进制的插件的对照

| | 编译进二进制 | 包 |
| --- | --- | --- |
| 怎么来 | 构建时 `PLUGINS=…` | 拖进后台 / 随部署自带 |
| 运行在 | 服务器进程里，与核心同权 | wazero 沙箱里，只有声明过的权限 |
| 卸载 | 状态变为「未安装」，代码还在二进制里 | 从数据库删掉，列表里没有了 |
| 语言 | Go，引用核心的 `internal/` | Go（或任何能编出 wasm 的语言），只引用 SDK |
| 出错 | 崩溃可能带走整个服务器 | 只有这次调用失败 |

## 备份与恢复

包在数据库里，所以[实例备份](../admin/backups-and-data)带得走它：`.arcbackup` 里有 `plugin_packages` 表，恢复时 `restore-backup` 用其中每个包自己的迁移把空数据库建成归档那个实例的样子，再导入数据。只建归档那个实例跑过的迁移——随部署自带、但这个实例没装过（或删除时清掉了数据）的插件不会在新库里留下多余的表。

唯一的例外是「删除时保留了数据」的插件：表在归档里，包不在。此时恢复会在写入前停下，指出缺的迁移；把它的 `.arcx` 放进 `OBSIDIAN_PLUGIN_DIR` 再运行。

## 多实例

包存在数据库里，第二个实例启动时就能读到；但在一个实例上装、删、改，其他实例要重启才看得到。这和编译进二进制的插件一样。
