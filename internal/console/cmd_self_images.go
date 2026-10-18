package console

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// ---------------------------------------------------------------------
// image list | delete
//
// The Image Lab's history. Generating a picture stays in the lab — a
// terminal cannot show one — but the history is a list the caller owns and
// may prune, and that is a change they should be able to make from here.
// Both routes are scoped to the caller by the handler.
// ---------------------------------------------------------------------

func init() {
	registerCommand(Command{
		Name:    "image list",
		Group:   "images",
		Summary: Text{EN: "List the images you have generated", ZH: "列出你生成过的图片"},
		Usage:   "image list [--limit N] [--before MS]",
		Flags: []Flag{
			{Name: "--limit", Hint: Text{EN: "page size, default 50", ZH: "每页数量，默认 50"}, Value: "N", Default: "50"},
			{Name: "--before", Hint: Text{EN: "epoch ms; older than this, for the next page", ZH: "毫秒时间戳；只看更早的，用于翻页"}, Value: "MS"},
		},
		Examples:   []string{"image list", "image list --limit 10 --json"},
		SeeAlso:    []string{"image delete"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/images/generations"},
		Run: func(_ context.Context, rt *Runtime) error {
			q := url.Values{"limit": {strconv.Itoa(rt.IntOr("limit", 50))}}
			if v := rt.String("before"); v != "" {
				q.Set("before", v)
			}
			data, _, err := rt.Call(http.MethodGet, "/api/images/generations?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, raw := range asSlice(asMap(data)["generations"]) {
				g := asMap(raw)
				rows = append(rows, []string{
					asStr(g["id"]), asStr(g["size"]), truncateForTable(asStr(g["prompt"]), 50),
					asStr(g["url"]), formatMS(g["created_at"]),
				})
			}
			return rt.Table([]string{"id", "size", "prompt", "url", "created"}, rows)
		},
	})

	registerCommand(Command{
		Name:    "image delete",
		Group:   "images",
		Summary: Text{EN: "Delete one image from your history", ZH: "从历史记录中删除一张图片"},
		Usage:   "image delete <id> --yes",
		Help: Text{
			EN: "Removes the picture and its entry, as the delete button in the Image Lab's gallery does. It cannot be undone.",
			ZH: "删除这张图片及其记录，和生图实验室图库里的删除按钮一样，无法撤销。",
		},
		Args:        []Arg{{Name: "id", Hint: Text{EN: "from image list", ZH: "来自 image list"}, Required: true}},
		Examples:    []string{"image delete 01H9Z… --yes", "image delete 01H9Z… -y"},
		SeeAlso:     []string{"image list"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"DELETE /api/images/generations/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "image id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/images/generations/"+url.PathEscape(ref), nil); err != nil {
				return err
			}
			return rt.confirm(Text{EN: "deleted.", ZH: "已删除。"})
		},
	})
}
