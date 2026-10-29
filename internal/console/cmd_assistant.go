package console

import (
	"context"
	"net/http"
)

func init() {
	registerCommand(Command{
		Name: "assistant ask", Group: "instance", Permission: "",
		Summary:   Text{EN: "Ask Rune, the administration assistant", ZH: "向 Rune 管理助手提问"},
		Usage:     "assistant ask <text> --model <id>",
		Help:      Text{EN: "Returns the assistant's answer and proposed commands. Proposed commands are not executed; review them before running them.", ZH: "返回助手的回答和建议命令。建议命令不会执行，请先检查再运行。"},
		Args:      []Arg{{Name: "text", Required: true, Hint: Text{EN: "question or task", ZH: "问题或任务"}, Sensitive: true}},
		Flags:     []Flag{{Name: "--model", Value: "ID", Hint: Text{EN: "model ID", ZH: "模型 ID"}}},
		Examples:  []string{"assistant ask 'Summarize recent usage' --model <id>", "assistant ask '检查模型可用性' --model <id> --json"},
		Endpoints: []string{"POST /api/admin/assistant"},
		Run: func(_ context.Context, rt *Runtime) error {
			if rt.String("model") == "" {
				return rt.Fail(Text{EN: "--model is required", ZH: "请指定 --model"})
			}
			_, _, err := rt.Call(http.MethodPost, "/api/admin/assistant", map[string]any{
				"model_id": rt.String("model"), "lang": rt.Session.Lang,
				"messages": []map[string]string{{"role": "user", "content": rt.Arg(0)}},
			})
			if err != nil {
				return err
			}
			return RenderJSON(rt.Out, rt.rawJSON)
		},
	})
}
