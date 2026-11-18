# 系统设置

后台系统设置保存到数据库，当前实例保存后即可使用新值。邮件服务也在后台配置：SMTP 密码加密保存，读取配置时只显示是否已设置。数据库连接、监听地址和实例密钥仍属于[启动配置](../guide/configuration)。

## 站点与注册

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `site.name` | `Obsidian Arc` | 站点名称 |
| `site.description` | 空 | 站点说明 |
| `site.browser_title` | 空 | 浏览器标签页标题；空值使用 `site.name` |
| `site.auth_card_position` | `center` | 登录与注册布局；`center`、`left`、`right` 为悬浮卡片；`left-panel`、`right-panel` 为约四成宽的左侧、右侧通高面板，另一侧保留默认背景；窄屏时面板铺满宽度。登录与注册之间切换时平滑展开、收起表单 |
| `about.title` / `about.body` | 空 | 关于页面标题与 Markdown 详细介绍；空值使用内置内容 |
| `home.notice` | 空 | 首页通知横条上的一行字（最多 300 字符），登录前也可见 |
| `home.notice_body` | 空 | 点开横条后的 Markdown 正文（最多 4000 字符） |
| `home.notice_tone` | `info` | 横条样式：`info` 或 `warning` |
| `home.notice_dismissible` | `true` | 是否允许关闭通知 |
| `update.check` | `true` | 超级管理员打开后台时，是否向 GitHub 查询新版本并在后台提醒；每 12 小时最多查询一次，关闭后服务器不会向 GitHub 发出任何请求 |
| `registration.enabled` | `true` | 是否开放注册 |
| `registration.default_group` | 空 | 注册分组覆盖值；未设置时使用默认组 |
| `registration.require_email` | `false` | 注册是否要求邮箱 |
| `registration.verify_email` | `false` | 是否要求邮件验证；开启后新账户必须提供邮箱，需先配置并测试邮件服务 |
| `registration.email_domains` | 空 | 允许注册的邮箱域名 |

注册频率、Turnstile 和模型审核见[注册与安全](../features/security)。

「安全 → 邮件服务」保存 SMTP 主机、端口、加密方式、账号、发件人及站点公开地址，并向指定地址发送测试邮件。先保存配置再测试；未保存的草稿不会用于测试邮件。密码输入留空表示保留原密码，清除密码需要明确选择。邮件把验证链接和六位验证码发给同一个收件人；链接需在页面点击确认，两者任一成功，另一种方式同时失效。

「安全 → 临时邮箱检测」可填写付费 UserCheck API 密钥并启用检测。后台预填一组常用邮箱域名免检列表，可增删；域名按完整名称匹配，免检域名完全跳过 UserCheck，包括对该域名中个别临时地址的识别。名单外的邮箱使用 [UserCheck 邮箱接口](https://www.usercheck.com/docs/api/email-endpoint)；只在返回 `disposable: true` 时拒绝。密钥加密保存，输入框留空会保留原密钥。管理员还可选择 UserCheck 超时、限流或不可用时让注册继续，或暂停注册并提示稍后重试；默认为后者。测试按钮会实际调用付费接口，五分钟内只能执行一次。

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `security.signup_review_restrict_hours` | `24` | `restrict` 注册的 API 限制时长；0 表示手动解除 |
| `security.chat_challenge_requests` | `0` | 聊天速度窗口内允许的请求数；0 表示关闭 |
| `security.chat_challenge_window_seconds` | `60` | 聊天速度统计窗口 |
| `security.chat_challenge_clear_minutes` | `30` | 一次验证通过后的免验证时间 |

## 浏览器标题与 PWA

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `pwa.name` | 空 | 安装提示中的应用名称；空值使用 `site.name` |
| `pwa.short_name` | 空 | 主屏幕图标下方显示的简称；空值使用上面解析出的应用名称 |
| `pwa.description` | 空 | 安装提示中的说明；空值使用 `site.description` |
| `pwa.theme_color` | `#18181b` | 浏览器外观着色，格式为 `#rrggbb` |
| `pwa.background_color` | `#18181b` | 应用加载时图标背后的颜色，格式为 `#rrggbb` |
| `pwa.icon_url` | 空 | 应用图标；只接受本站的相对路径或 `data:` 内联图片，空值使用内置图标 |

`site.browser_title` 决定浏览器标签页标题，已登录浏览器会在保存后立即更新，无需刷新页面。`pwa.*` 决定安装为应用（PWA）时的名称、说明和外观，由 `GET /manifest.webmanifest` 提供给浏览器，该接口无需登录即可访问。

图标地址的限制并非疏漏：页面的图片安全策略只允许本站路径和内联的 `data:` 图片，指向其他域名的地址会被浏览器静默忽略，因此保存时直接拒绝。

## 首页与对话

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `landing.mode` | `login` | 登录页、内置官网首页、介绍页或访客对话 |
| `landing.intro` | 空 | 介绍页 HTML，经过允许列表处理 |
| `landing.trial_enabled` | `false` | 开启访客试用 |
| `landing.trial_turns` | `3` | 试用轮数，最高 20 |
| `landing.trial_model` | 空 | 试用模型 |
| `chat.default_system_prompt` | 空 | 默认系统提示词 |
| `chat.max_turns` | `40` | 发送给上游的历史范围上限 |
| `chat.allow_archive` | `true` | 是否允许用户归档会话 |
| `chat.web_search` | `false` | 是否在输入框旁提供联网搜索开关 |
| `chat.web_search_endpoint` | `https://www.bing.com/search` | 搜索地址；返回 JSON 的 SearXNG 兼容服务也可 |
| `chat.web_search_results` | `5` | 每次交给模型的结果数，1–10 |

访客试用只在 `landing.mode=chat` 时提供。`site` 是程序自带的官网首页，内容由程序提供，不读取 `landing.intro`；介绍页支持受控 HTML，首页通知则是纯文本，三者格式不同。详见[访客试用](../features/trial)。

## API 与额度

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `api.enabled` | `false` | 全站 API 开关，也限制管理员 |
| `quota.admins_bypass` | `true` | 管理员是否绕过额度限制 |
| `quota.usage_display` | `absolute` | 具体数值、已用比例或剩余比例；额度窗口和赠金条都按它措辞 |
| `quota.max_concurrent` | `4` | 全站最大并发流式请求数限制；0 表示不限 |

API 开关与额度豁免是两项独立设置。用户组还需要 `api_access` 权限；额度上限在全局、用户组或用户策略中配置。

## 工作台草稿与保存

系统设置在当前表单中标记未保存的更改。保存时只提交改动的字段。切换后台页面、刷新或关闭标签页前，请先保存要保留的修改。

## 附件保留与清理

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `attachments.max_mb` | `6` | 单文件大小上限，最高 64 MB；同样适用于 /v1 接口的图片 |
| `attachments.retain` | `false` | 发送后是否保留上传图片数据 |
| `attachments.purge_after_days` | `0` | 按年龄清理；0 不启用 |
| `attachments.purge_daily_at` | 空 | 每日清理时间，格式 `HH:MM` |
| `attachments.orphan_minutes` | `60` | 未关联消息的附件保留分钟数 |

开启保留后，按天与定时清理仍可删除附件数据。每日清理不是普通的临时文件整理：确认要清理的范围后再启用。

自动清理由后台周期任务执行，不保证恰好在指定分钟开始。手动清理前也应阅读界面确认内容。

## 导入与导出

设置可以导出为 JSON，再通过导入入口应用到实例。导入会校验可写字段，对不适用内容给出处理结果。

设置导出不是实例备份，不包含完整的用户、会话和服务商凭据。恢复站点数据应使用[数据库备份](./backups-and-data)。

## 多实例注意事项

每个节点每两秒检查数据库中的配置版本，加载已变更的业务设置、插件、SMTP 和 UserCheck 配置。关闭公开注册会在下一次开户事务中立即生效。插件清除数据会删除数据库列，应先停用插件，让其他节点完成刷新后再清除数据。

## 注册审核与身份迁移

模型审核默认覆盖本站表单注册。开启「同时审核第三方注册的新账号」后，OAuth 首次开户使用相同的模型、决策和费用预算。关联已有账号和后续登录无需审核。所有节点每分钟合计最多发起 120 次注册审核，每个来源地址最多 10 次。同一地址和用户名间隔至少 5 秒。

OIDC 连接绑定到发行方及其 subject。显式配置端点时，端点组合与 issuer 共同标识身份源，即使保留相同的 issuer，切换手动端点也需要重新绑定。更改身份源后，用户可通过另一种方式登录，在账户设置中重新连接。升级前的 OIDC 连接缺少发行方记录。对仅有 OIDC 登录方式的用户，升级前应验证其备用登录方式，或由管理员依据旧身份源的可信记录核实账号归属，再定向恢复登录。管理员可对已核实的用户运行 `user passwd <用户名> --generate --yes`，该操作会结束该账号的现有会话。用户用新密码登录后，在账户设置中重新连接身份源。仅使用发行方自动发现、已核实原始 issuer 的旧连接，也可在维护窗口按核实的账号和 subject 将 `oauth_identities.issuer` 从空值设置为旧发行方地址。已进行中的 OIDC 流程需要重新开始。

升级 0034 之前的实例会保留原用户组模型授权。已经运行旧版 0034 的实例，管理者需对照升级前备份或授权记录核对各组的模型使用权限。

敏感设置和服务商自定义请求头使用实例密钥加密。实例级备份保留密文，恢复时使用原实例密钥。迁移前生成的备份保留其原有格式。
