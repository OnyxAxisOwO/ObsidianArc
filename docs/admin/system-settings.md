# 系统设置

后台系统设置保存到数据库，当前实例保存后即可使用新值。数据库连接、SMTP 凭据、监听地址和实例密钥属于[环境变量](../guide/configuration)，需要重启后生效。

## 站点与注册

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `site.name` | `Obsidian Arc` | 站点名称 |
| `site.description` | 空 | 站点说明 |
| `about.title` / `about.body` | 空 | 关于页面；空值使用内置内容 |
| `home.notice` | 空 | 首页纯文本通知 |
| `home.notice_dismissible` | `true` | 是否允许关闭通知 |
| `registration.enabled` | `true` | 是否开放注册 |
| `registration.default_group` | 空 | 注册分组覆盖值；未设置时使用默认组 |
| `registration.require_email` | `false` | 注册是否要求邮箱 |
| `registration.verify_email` | `false` | 是否要求邮件验证，需邮件服务可用 |
| `registration.email_domains` | 空 | 允许注册的邮箱域名 |
| `registration.qq_requirement` | `off` | QQ 关闭、选填或必填 |

注册频率、Turnstile 和模型审核见[注册与安全](../features/security)。

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `security.signup_review_restrict_hours` | `24` | `restrict` 注册的 API 限制时长；0 表示手动解除 |
| `security.chat_challenge_requests` | `0` | 聊天速度窗口内允许的请求数；0 表示关闭 |
| `security.chat_challenge_window_seconds` | `60` | 聊天速度统计窗口 |
| `security.chat_challenge_clear_minutes` | `30` | 一次验证通过后的免验证时间 |

## 首页与对话

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `landing.mode` | `login` | 登录页、介绍页或访客对话 |
| `landing.intro` | 空 | 介绍页 HTML，经过允许列表处理 |
| `landing.trial_enabled` | `false` | 开启访客试用 |
| `landing.trial_turns` | `3` | 试用轮数，最高 20 |
| `landing.trial_model` | 空 | 试用模型 |
| `chat.default_system_prompt` | 空 | 默认系统提示词 |
| `chat.max_turns` | `40` | 发送给上游的历史范围上限 |

访客试用只在 `landing.mode=chat` 时提供。介绍页支持受控 HTML，首页通知则是纯文本，两者格式不同。

## API 与额度

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `api.enabled` | `false` | 全站 API 开关，也限制管理员 |
| `quota.admins_bypass` | `true` | 管理员是否绕过额度限制 |
| `quota.usage_display` | `absolute` | 具体数值、已用比例或剩余比例 |

API 开关与额度豁免是两项独立设置。用户组还需要 `api_access` 权限；额度上限在全局、用户组或用户策略中配置。

## 附件保留与清理

| 设置键 | 默认值 | 用途 |
| --- | --- | --- |
| `attachments.max_mb` | `6` | 单文件大小上限，最高 64 MB |
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

业务设置在进程内缓存。当前实现没有跨进程自动刷新机制：通过一个实例保存设置，不代表其他实例的内存值也立即更新。

多实例部署时需要明确安排重新加载或重启，不能把数据库已经更新当作所有进程已同步。
