# 终端

**终端**是一个命令行界面，在右上角的账户菜单里打开，每个账号都能用。它不是一个新的权限体系：每条命令都是 API 的调用方，走的是和界面完全相同的那套接口、那套鉴权中间件、那套逐路由的权限判断。

所以每个人在终端里能做的事，正好等于他在自己的页面上能做的事：

- **普通用户**：自己的资料、密码、偏好、API 密钥、对话、项目、额度与卡密、反馈、生图记录、备份。
- **管理员**：以上全部，再加上他持有的那几项后台授权对应的管理命令。
- **超级管理员**：和后台界面一样的完整范围，不多也不少。

同一个终端还可以通过 SSH 访问，不需要浏览器。

::: tip 从后台搬出来了
终端原来是管理后台左侧栏里的一页，只有管理员能用。现在它在账户菜单里；旧地址 `/admin/terminal` 会自动跳到 `/terminal`。
:::

## 谁能用

默认所有人都能用。管理员可以按**用户组**关掉：后台「用户组」→ 编辑某个组 →「成员权限」里的**可以使用终端**。

- 关掉后，这个组的成员在账户菜单里看不到终端，直接访问接口也会被拒绝（`terminal_not_permitted`）。已经打开的终端在下一条命令时就会被拒，不用等他关页面。
- **管理员始终可用**，不受所在用户组的设置影响——后台有些工作就是在终端里做的，一个组设置不应该把运营者自己锁在外面。
- SSH 入口用的是同一条规则：组里关掉了终端的账号，SSH 登录同样会失败，提示和密码错误完全一致。

终端里也能改这个设置：

```bash
group edit Default --terminal false
```

## 它能做什么

终端覆盖全部 106 个后台管理接口，以及账户页面能调用的 79 个用户端接口中凡是「设置」或「更改」的那部分，共 179 条命令。按功能分组：

| 分组 | 命令 | 适用对象 |
| --- | --- | --- |
| 账户 (Accounts) | `user list` `show` `create` `edit` `delete` `passwd` `reset-2fa` `keys` `key-revoke` `chats` `transcript` `cards` | 管理员 (`users`) |
| 用户组 (Groups) | `group list` `show` `create` `edit` `delete` `members` `assign` | 管理员 (`groups`) |
| 邀请码 (Invites) | `invite list` `create`（含 `--partner`）`revoke` `uses` `stats` | 管理员 (`invites`) |
| 服务商与模型 (Catalogue) | `provider list` `show` `create` `edit` `delete` `detect` · `model list` `show` `create` `edit` `delete` `order` `import` | 管理员 (`providers`/`models`) |
| 运维 (Operations) | `dash` `res` · `health status` `probe` `reset` · `usage summary` `breakdown` `rpm` `records` `reset` · `quota list` `set` `delete` · `code list` `create` `redemptions` `delete` · `log list` `facets` `prune` | 管理员 (`dashboard`/`resources`/`availability`/`usage`/`codes`/`logs`) |
| 实例 (Instance) | `setting list` `get` `set` `import` · `notice list` `create` `edit` `delete` · `security events` `review` `two-factor` · `attachment purge` · `meta` `refs` `member-options` | 管理员 (`settings`/`announcements`/`security`) |
| 用户反馈 (Feedback) | 处理别人的反馈：`feedback list` `show` `reply` `resolve` `reopen` `delete` | 管理员 (`feedback`) |
| | 自己的反馈：`feedback send` `mine` `thread` `answer` `unread` | 所有用户 |
| 会话 (Session) | `help` `clear` `exit` `whoami` `version` `history` `watch` `lang` `format` `echo` | 所有用户 |
| 对话 (Chat) | `chat list` `show` `rename` `edit` `delete` `delete-all` `archive` `unarchive` | 所有用户 |
| 项目 (Projects) | `project list` `show` `create` `edit` `delete` | 所有用户 |
| 生图 (Images) | `image list` `delete` | 所有用户 |
| 赠金 (Bonus) | `bonus list` `create` `edit` `delete` `grant` `grants` `revoke` · `checkin config` `config set`，见[赠金与签到](./bonus-and-checkin) | 管理员 (`usage`) |
| 额度与卡密 (Credit) | `credit show` `history` `cards` `use` `redeem` `bonus` `bonus switch` · `checkin` `checkin now` `checkin claim` | 所有用户 |
| API 密钥 (Keys) | `key list` `create` `edit` `delete` | 所有用户 |
| 偏好与资料 (Profile) | `pref list` `set` `wallpaper-clear` · `me show` `edit` `passwd` `verify` `invite` `invite regenerate` `invite claim` · `2fa status` `setup` `enable` `disable` `recovery` `backoffice` · `oauth list` `unlink` | 所有用户 |
| 备份与恢复 (Backup) | `backup export` `backup import` | 所有用户 |

完整列表以 `help` 为准——它只列出**你当前有权执行**的命令，不会把未授权的命令呈现出来。每个命令均可通过 `<命令> --help` 或 `help <命令>` 查看中英双语的详细参数、用法和示例。

几个值得一提的：

- `usage breakdown user --model <模型>`：某个模型有哪些人在用；`usage breakdown model --user <用户>`：某个人在用哪些模型；`usage breakdown model --metric users`：按使用人数排出最受欢迎的模型。
- `chat edit <对话> <序号> --content "…"`：改写对话里的某条消息，序号就是 `chat show` 打印的 `seq`。只改记录，不会重新发给模型。
- `feedback mine` / `feedback thread <id>`：看自己的反馈和回复，读过之后账户菜单上的小红点就会消失。
- `invite create --partner NAME --code CODE --group REF --group-days N-M`：生成一个合作方专属码，默认老用户也能领取；`me invite claim <码>` 是任何账户领取这类码（或显式开放了「老用户也能领取」的批量码）的入口，见[邀请码](./invites)。

### 没有做成命令的

下面这些是账户页面会调用、但终端里故意没有对应命令的接口，原因都不是「忘了」：

| 接口 | 为什么没有 |
| --- | --- |
| 登录、登出、注册、邮箱验证、登录时的两步验证码 | 终端本身就是登录之后才打开的；登出请用账户菜单 |
| 第三方登录的跳转与注册 | 需要浏览器跟随跳转 |
| 发送聊天消息、生成图片 | 对话和画图在聊天界面和生图实验室里进行，终端显示不了图片 |
| 上传附件、上传壁纸、读取图片 | 终端没法选择或显示图片；壁纸可以用 `pref wallpaper-clear` 移除 |
| 站点公开信息 | 登录页用的只读信息，不是设置 |

这份清单写在测试里（`internal/console/commands_test.go` 的 `consoleExempt`）：以后账户页面新增一个可以设置或更改的接口，如果终端里没有对应命令、也没有写进这张表并说明理由，构建会直接失败。后台接口也有同样的检查。

## 权限

终端不会扩大任何人的权限，这一点是由结构保证的，而不是靠另写一份检查：命令并不直接读写数据库，而是在进程内向 API 发起一次请求，因此登录校验、所有权检查和每条后台路由自己的权限包装都照常执行。

后果是：

- 普通用户运行 `user list` 会被拒绝，而且在 `help` 和 Tab 补全里根本看不到这些命令。
- 只持有 `users` 授权的管理员，在终端里同样只能操作账户。`model list` 会被拒绝，`setting set` 也会。
- 超级管理员在终端里拥有和后台界面一样的完整范围。

被拒绝时的提示会直接说明缺少哪一项授权：

```
permission denied: this command needs the "models" grant
```

## 危险操作

所有不可逆的命令（每一个 `delete`、`user passwd`、`log prune`、`attachment purge`、`health reset`、`usage reset`、`setting import`）都必须显式加 `--yes`（或 `-y`）才会执行。缺少时命令拒绝执行并说明原因，不会有确认弹窗。

## 网页终端

右上角账户菜单 →**终端**。它在对话旁边打开成一栏，和设置、API 密钥这些面板一样：对话列表会让出位置，而不是被盖住。面板可以拖宽，右上角的按钮可以切到全屏。

- 顶部是标签栏，可以开多个标签页。标签数量变多时会自行收缩，不会顶出容器。双击可重命名，`×` 关闭。
- 右侧的齿轮是外观设置：字体、字号、行高、字符间距、光标样式与闪烁、回滚缓冲区行数、时间戳、连字。设置保存在浏览器本地。
- 终端的透明度和模糊**不在这里设置**，它跟随全局的壁纸设置，和其他面板保持一致。
- 终端的代码只有在第一次打开时才会下载，不打开就不占首屏体积。

键盘：

| 按键 | 作用 |
| --- | --- |
| `Enter` | 执行 |
| `Shift+Enter` | 换行（粘贴多行 JSON 时用得上） |
| `↑` / `↓` | 翻阅历史 |
| `Tab` | 补全 |
| `Ctrl+L` | 清屏 |
| `Esc` / `Ctrl+C` | 取消正在执行的命令 |
| `Alt+T` / `Alt+W` | 新建 / 关闭标签页 |

`Ctrl+T` 和 `Ctrl+W` 归浏览器所有，因此新建和关闭标签页用 `Alt`。

## 通过 SSH 连接

设置 `OBSIDIAN_SSH_ADDR` 后（例如 `:2222`），可以不开浏览器直接连接终端：

```bash
ssh 用户名@你的域名 -p 2222
```

**连上的是终端，不是服务器。** 这个监听端口只能执行终端命令，没有 shell，没有文件传输，没有端口转发——服务端代码里根本没有通向 `os/exec` 的路径。

谁能通过 SSH 登录，和谁能打开网页终端是同一条规则：管理员，以及用户组允许使用终端的普通账号。认证使用和网页登录相同的密码，共用同一套失败次数限制，所以在两个入口猜密码花的是同一份预算。不允许的账号会被拒绝，提示和密码错误完全一致，没法借此探测谁有权限。每条命令执行前都会重新读取账号，权限被收回、账号被停用或组里关掉了终端，下一条命令就会断开。

也可以只跑一条命令，这让终端可以被脚本调用：

```bash
ssh 用户名@你的域名 -p 2222 'key list --json' | jq '.keys[].name'
```

所有命令都支持 `--json`，输出的是 API 的原始载荷。

主机密钥在首次启动时生成并保存在数据目录（`ssh_host_ed25519_key`，权限 0600），指纹会打印在启动日志里，也可以用 `help ssh` 查看——首次连接时请核对。数据目录丢失会导致指纹变化，客户端的 known_hosts 校验会因此报警。

相关配置项见[配置参考](../guide/configuration)。

## 审计

每一条改变状态的命令都会记入安全事件（`console_command`），包含执行人、来源（`web` 或 `ssh`）、IP 和命令原文。`--password`、`--api-key` 这类敏感参数在记录前已被替换为 `***`。

纯查询类的会话命令（`help`、`whoami`、`version` 等）不记录。

在[安全](../admin/overview)页或用 `security events --event console_command` 查看。

## help

`help` 是终端的说明书，中英双语，按当前会话语言显示：

| 命令 | 作用 |
| --- | --- |
| `help` | 按分组列出你能执行的命令 |
| `help <命令>` | 用法、参数、选项、示例、所需权限、调用的接口 |
| `help <名词>` | 列出该名词下的所有动作，如 `help chat` |
| `help -k <关键字>` | 搜索命令，中英文均可 |
| `help keys` | 键盘快捷键 |
| `help ssh` | 如何通过 SSH 连接，含主机指纹 |

任意命令加 `--help` 等同于 `help <命令>`。

SSH 会话的语言取自客户端传来的 `LANG`：

```bash
ssh -o SetEnv=LANG=zh_CN.UTF-8 用户名@你的域名 -p 2222 'help chat edit'
```

网页终端跟随界面语言。会话内也可以用 `lang zh` / `lang en` 临时切换。
