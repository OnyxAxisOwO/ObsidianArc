package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------------
// key list | create | edit | delete
//
// Every route here is /api/keys, never /api/admin/*: the handlers in
// internal/apikey scope every read and write to auth.MustUser(ctx) already,
// so Permission: Anyone is correct precisely because there is no id to
// widen the blast radius with — a caller can only ever name their own key,
// the same way the web settings screen can.
// ---------------------------------------------------------------------

// modelIDsStr renders a key's model_ids for a table or Fields block. Empty
// is a real, distinct answer ("any model this account may use," per
// apikey.Key's own doc comment) and must not be confused with a single
// unrestricted-looking blank column.
func modelIDsStr(v any) string {
	ids := asSlice(v)
	if len(ids) == 0 {
		return "-"
	}
	out := make([]string, len(ids))
	for i, raw := range ids {
		out[i] = asStr(raw)
	}
	return strings.Join(out, ",")
}

func init() {
	registerCommand(Command{
		Name:    "key list",
		Group:   "keys",
		Summary: Text{EN: "List your own API keys", ZH: "列出你自己的 API 密钥"},
		Usage:   "key list",
		Help: Text{
			EN: "Never shows a token: the server only ever returns one, once, from key create.",
			ZH: "不会显示密钥本身：服务器只会在 key create 时返回一次。",
		},
		Examples:   []string{"key list", "key list --json"},
		SeeAlso:    []string{"key create", "key edit", "key delete"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/keys"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/keys", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			keys := asSlice(m["keys"])
			rows := make([][]string, 0, len(keys))
			for _, raw := range keys {
				k := asMap(raw)
				rows = append(rows, []string{
					asStr(k["id"]), asStr(k["prefix"]), asStr(k["name"]),
					yesNo(asBoolVal(k["disabled"])), modelIDsStr(k["model_ids"]),
					formatMS(k["expires_at"]), formatMS(k["last_used_at"]),
				})
			}
			if err := rt.Table([]string{"id", "prefix", "name", "disabled", "model_ids", "expires", "last_used"}, rows); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return nil
			}
			// enabled/max are top-level fields of the same response, not a
			// row — shown the way "user show" appends cards and quota below
			// its own Fields block, rather than forcing them into the table.
			canCreate, keyMax := yesNo(asBoolVal(m["enabled"])), asNum(m["max"])
			rt.Say(Text{
				EN: fmt.Sprintf("\ncan create new keys: %s, max: %v\n", canCreate, keyMax),
				ZH: fmt.Sprintf("\n可创建新密钥：%s，上限：%v\n", canCreate, keyMax),
			})
			return nil
		},
	})

	registerCommand(Command{
		Name:    "key create",
		Group:   "keys",
		Summary: Text{EN: "Create a new API key for your own account", ZH: "为你自己的账户创建一个新的 API 密钥"},
		Usage:   "key create --name TEXT [--model IDS] [--expires-at MS]",
		Help: Text{
			EN: "The token is returned exactly once, in this command's own output — the server keeps " +
				"only a digest of it and cannot show it again, so save it now. --model takes one or " +
				"more model ids, comma-separated, the same ids the chat's model picker uses; left " +
				"empty, the key may use any model your account already can. If the operator has " +
				"turned on a verification challenge for key creation, this command cannot pass one and " +
				"the call fails; create the key from the web settings screen instead.",
			ZH: "令牌只会在这条命令自己的输出中出现一次——服务器只保存它的摘要，之后无法再次显示，请立即保存。" +
				"--model 接受一个或多个模型 id，用逗号分隔，与聊天界面模型选择器所用的 id 相同；留空则该密钥" +
				"可使用你账户本就能使用的任意模型。如果运营方为创建密钥开启了验证挑战，这条命令无法提交挑战" +
				"结果，调用会失败，请改用网页设置界面创建。",
		},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "1-60 characters", ZH: "1-60 字符"}, Value: "TEXT"},
			{Name: "--model", Hint: Text{EN: "comma-separated model ids, default any", ZH: "逗号分隔的模型 id，默认为任意"}, Value: "IDS"},
			{Name: "--expires-at", Hint: Text{EN: "epoch ms, 0 or absent = never", ZH: "毫秒时间戳，0 或不填表示永不过期"}, Value: "MS"},
		},
		Examples:   []string{"key create --name laptop", "key create --name ci --model 01H9Z… --expires-at 1735689600000"},
		SeeAlso:    []string{"key list", "key delete"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/keys"},
		Run: func(_ context.Context, rt *Runtime) error {
			name := rt.String("name")
			if name == "" {
				return rt.Fail(Text{EN: "--name is required", ZH: "需要 --name"})
			}

			body := bodyBuilder{}
			body.str(rt, "name", "name")
			if rt.Present("model") {
				body["model_ids"] = splitCSV(rt.String("model"))
			}
			body.int64v(rt, "expires-at", "expires_at")

			data, _, err := rt.Call(http.MethodPost, "/api/keys", map[string]any(body))
			if err != nil {
				return err
			}
			m := asMap(data)
			k := asMap(m["key"])
			if err := rt.Fields([][2]string{
				{"id", asStr(k["id"])},
				{"prefix", asStr(k["prefix"])},
				{"name", asStr(k["name"])},
				{"model_ids", modelIDsStr(k["model_ids"])},
				{"expires_at", formatMS(k["expires_at"])},
				{"created_at", formatMS(k["created_at"])},
			}); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				// The server's own response body, already carrying "token"
				// in the clear — Fields printed it verbatim above, so
				// there is nothing left to add.
				return nil
			}
			token := asStr(m["token"])
			rt.Say(Text{EN: fmt.Sprintf("\ntoken (save it now — this is the only time it is shown in full): %s\n", token), ZH: fmt.Sprintf("\ntoken（现在就保存它——这是它唯一一次完整显示）：%s\n", token)})
			return nil
		},
	})

	registerCommand(Command{
		Name:    "key edit",
		Group:   "keys",
		Summary: Text{EN: "Rename or pause one of your own API keys", ZH: "重命名或暂停你自己的一个 API 密钥"},
		Usage:   "key edit <id> [--name TEXT] [--enabled BOOL]",
		Help: Text{
			EN: "Only the flags you give are changed. --enabled false pauses the key without deleting " +
				"it — every request made with it is refused until it is re-enabled or deleted; " +
				"--enabled true reverses that.",
			ZH: "只会修改你给出的选项。--enabled false 会暂停密钥而不删除它——在此期间用它发起的每个请求都会被拒绝，" +
				"直到重新启用或删除为止；--enabled true 则会恢复它。",
		},
		Args: []Arg{
			{Name: "id", Hint: Text{EN: "key id, from key list", ZH: "密钥 id，来自 key list"}, Required: true},
		},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "1-60 characters", ZH: "1-60 字符"}, Value: "TEXT"},
			{Name: "--enabled", Hint: Text{EN: "true to re-enable, false to pause", ZH: "true 重新启用，false 暂停"}, Value: "BOOL"},
		},
		Examples:   []string{"key edit 01H9Z… --name laptop", "key edit 01H9Z… --enabled false"},
		SeeAlso:    []string{"key list", "key delete"},
		Permission: Anyone,
		Endpoints:  []string{"PATCH /api/keys/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			keyID, err := requireRef(rt, "key id")
			if err != nil {
				return err
			}

			// --enabled is the flag's own polarity (matching the web
			// settings toggle); the wire field is "disabled", so the two
			// are inverted right here rather than asking every future
			// reader of the API contract to remember the flip.
			body := bodyBuilder{}
			body.str(rt, "name", "name")
			if rt.Present("enabled") {
				body["disabled"] = !rt.Bool("enabled")
			}
			if len(body) == 0 {
				return rt.Fail(Text{EN: "nothing to change: give at least one flag", ZH: "没有需要修改的内容：请至少给出一个选项"})
			}

			data, _, err := rt.Call(http.MethodPatch, "/api/keys/"+url.PathEscape(keyID), map[string]any(body))
			if err != nil {
				return err
			}
			k := asMap(data)
			return rt.Fields([][2]string{
				{"id", asStr(k["id"])},
				{"prefix", asStr(k["prefix"])},
				{"name", asStr(k["name"])},
				{"disabled", yesNo(asBoolVal(k["disabled"]))},
				{"model_ids", modelIDsStr(k["model_ids"])},
				{"expires_at", formatMS(k["expires_at"])},
				{"updated_at", formatMS(k["updated_at"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "key delete",
		Group:   "keys",
		Summary: Text{EN: "Delete one of your own API keys", ZH: "删除你自己的一个 API 密钥"},
		Usage:   "key delete <id> --yes",
		Help: Text{
			EN: "Revokes the key immediately. There is nothing to undo: the usage it already spent " +
				"stays on your ledger, and the key itself is gone rather than merely paused.",
			ZH: "会立即吊销该密钥，且无法撤销：它已产生的用量仍保留在你的账单中，但密钥本身会被彻底删除，而不是仅仅暂停。",
		},
		Args: []Arg{
			{Name: "id", Hint: Text{EN: "key id, from key list", ZH: "密钥 id，来自 key list"}, Required: true},
		},
		Examples:    []string{"key delete 01H9Z… --yes", "key delete 01H9Z… -y"},
		SeeAlso:     []string{"key list", "key edit"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"DELETE /api/keys/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			keyID, err := requireRef(rt, "key id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/keys/"+url.PathEscape(keyID), nil); err != nil {
				return err
			}
			return rt.confirm(Text{EN: "deleted.", ZH: "已删除。"})
		},
	})
}
