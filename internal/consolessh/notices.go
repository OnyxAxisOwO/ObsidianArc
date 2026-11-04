package consolessh

import "github.com/OnyxAxisOwO/ObsidianArc/internal/console"

// The lines this transport writes to a person at the console. The console engine
// translates its own output on Session.Lang; these are the strings this package
// writes itself, kept in the same Text type so they follow the same session
// language through Text.For, and so a missing language is caught by the same
// test that checks every other notice.

// Framed with CRLF because the pty is in raw mode, where a bare LF moves the
// cursor down without returning it to the start of the line.
var (
	// noticeRevoked: the account lost console access, demoted, disabled or deleted.
	noticeRevoked = console.Text{
		EN: "\r\nThis account no longer has console access. Closing.\r\n",
		ZH: "\r\n此账户已无控制台访问权限，正在关闭。\r\n",
	}
	// noticeCredentialChanged names the one reason a person can act on: the
	// password they signed in with is no longer the account's.
	noticeCredentialChanged = console.Text{
		EN: "\r\nThe password for this account changed. Sign in again.\r\n",
		ZH: "\r\n此账户的密码已更改，请重新登录。\r\n",
	}
	// noticeRetry says the check did not finish, which is not the same as the
	// account having lost access.
	noticeRetry = console.Text{
		EN: "\r\nCould not verify this account right now. Try again.\r\n",
		ZH: "\r\n暂时无法验证此账户，请稍后重试。\r\n",
	}
	// noticeInternalError is what a client sees when a goroutine serving its
	// session panicked. The panic value is logged, not sent: it can name a secret.
	noticeInternalError = console.Text{
		EN: "\r\nSomething went wrong on our side.\r\n",
		ZH: "\r\n服务器内部出错。\r\n",
	}
	noticeTypeAhead = console.Text{
		EN: "\r\n[input dropped: too much was typed while the command ran]\r\n",
		ZH: "\r\n[输入已丢弃：命令运行期间输入过多]\r\n",
	}
	noticeIdle = console.Text{
		EN: "\r\nidle timeout; closing.\r\n",
		ZH: "\r\n空闲超时，正在关闭。\r\n",
	}
	// noticeLineTooLong takes the line bound as its %d.
	noticeLineTooLong = console.Text{
		EN: "\r\n[input dropped: the line is longer than %d characters]\r\n",
		ZH: "\r\n[输入已丢弃：单行超过 %d 个字符]\r\n",
	}
	// noticeLineNotRun takes the line bound as its %d. Written where Enter
	// would have echoed its newline, because a line that lost runes is refused
	// rather than cut and run.
	noticeLineNotRun = console.Text{
		EN: "\r\n[line not run: it is longer than %d characters]\r\n",
		ZH: "\r\n[未运行：单行超过 %d 个字符]\r\n",
	}
	// noticeCommandStopped takes the exec timeout as its %s, and goes to stderr,
	// so it has no CRLF framing.
	noticeCommandStopped = console.Text{
		EN: "command stopped after %s\n",
		ZH: "命令已在 %s 后停止\n",
	}
)
