package console

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The "projects" family is an Anyone-tier noun: every command reaches a route
// mounted under auth.RequireUser on /api/projects, scoped to the caller by the
// store. A project is a standing brief and name whose instructions are inherited
// by conversations started inside it.
func init() {
	registerCommand(Command{
		Name:    "project list",
		Group:   "projects",
		Summary: Text{EN: "List your projects", ZH: "列出你的项目"},
		Usage:   "project list",
		Help: Text{
			EN: "Lists projects you own, newest first.",
			ZH: "按最近更新时间倒序列出你拥有的项目。",
		},
		Examples:   []string{"project list", "project list --json"},
		SeeAlso:    []string{"project show", "project create"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/projects"},
		Run: func(_ context.Context, rt *Runtime) error {
			data, _, err := rt.Call(http.MethodGet, "/api/projects", nil)
			if err != nil {
				return err
			}
			m := asMap(data)
			var rows [][]string
			for _, raw := range asSlice(m["projects"]) {
				p := asMap(raw)
				rows = append(rows, []string{
					asStr(p["id"]), asStr(p["name"]),
					fmt.Sprint(asNum(p["conversations"])), formatMS(p["updated_at"]),
				})
			}
			if err := rt.Table([]string{"id", "name", "conversations", "updated"}, rows); err != nil {
				return err
			}
			if rt.effectiveJSON() {
				return nil
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("\n项目数：%d，上限：%v\n", len(rows), asNum(m["max"]))
			} else {
				rt.Printf("\nprojects: %d, max: %v\n", len(rows), asNum(m["max"]))
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "project show",
		Group:   "projects",
		Summary: Text{EN: "Show one of your projects in full", ZH: "完整显示你的某个项目"},
		Usage:   "project show <project-id>",
		Help: Text{
			EN: "Shows project details including name, prompt instructions, conversation count and timestamps.",
			ZH: "显示项目详情，包括名称、指令提示词、会话数量和时间戳。",
		},
		Args: []Arg{
			{Name: "project-id", Hint: Text{EN: "from project list", ZH: "来自 project list"}, Required: true},
		},
		Examples:   []string{"project show 01H9Z…", "project show 01H9Z… --json"},
		SeeAlso:    []string{"project list", "project edit"},
		Permission: Anyone,
		Endpoints:  []string{"GET /api/projects/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "project id")
			if err != nil {
				return err
			}
			data, _, err := rt.Call(http.MethodGet, "/api/projects/"+url.PathEscape(ref), nil)
			if err != nil {
				return err
			}
			p := asMap(data)
			jsonMode := rt.effectiveJSON()
			if err := rt.Fields([][2]string{
				{"id", asStr(p["id"])},
				{"name", asStr(p["name"])},
				{"conversations", fmt.Sprint(asNum(p["conversations"]))},
				{"created_at", formatMS(p["created_at"])},
				{"updated_at", formatMS(p["updated_at"])},
			}); err != nil {
				return err
			}
			if jsonMode {
				return nil
			}
			if instructions := asStr(p["instructions"]); instructions != "" {
				rt.Printf("\ninstructions:\n%s\n", instructions)
			}
			return nil
		},
	})

	registerCommand(Command{
		Name:    "project create",
		Group:   "projects",
		Summary: Text{EN: "Create a new project", ZH: "创建新项目"},
		Usage:   "project create --name TEXT [--instructions TEXT]",
		Help: Text{
			EN: "Creates a project. Conversations started in this project inherit its instructions as part of their prompt.",
			ZH: "创建一个项目。在该项目中发起的对话将继承其指令提示词作为系统提示词的一部分。",
		},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "project name, ≤80 chars", ZH: "项目名称，≤80 字符"}, Value: "TEXT"},
			{Name: "--instructions", Hint: Text{EN: "system instructions inherited by conversations, ≤8000 chars", ZH: "对话继承的系统指令提示词，≤8000 字符"}, Value: "TEXT"},
		},
		Examples: []string{
			`project create --name "Code Review"`,
			`project create --name "Writing Assistant" --instructions "Keep replies concise."`,
		},
		SeeAlso:    []string{"project list", "project show"},
		Permission: Anyone,
		Endpoints:  []string{"POST /api/projects"},
		Run: func(_ context.Context, rt *Runtime) error {
			name := strings.TrimSpace(rt.String("name"))
			if name == "" {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("项目需要一个名称：请提供 --name")
				}
				return rt.Errorf("a project needs a name: provide --name")
			}
			body := map[string]any{"name": name}
			if rt.Present("instructions") {
				body["instructions"] = rt.String("instructions")
			}
			data, _, err := rt.Call(http.MethodPost, "/api/projects", body)
			if err != nil {
				return err
			}
			p := asMap(data)
			return rt.Fields([][2]string{
				{"id", asStr(p["id"])},
				{"name", asStr(p["name"])},
				{"created_at", formatMS(p["created_at"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "project edit",
		Group:   "projects",
		Summary: Text{EN: "Edit a project's name or instructions", ZH: "修改项目的名称或指令"},
		Usage:   "project edit <project-id> [--name TEXT] [--instructions TEXT]",
		Help: Text{
			EN: "Only the flags you give are changed — an absent flag leaves that field alone.",
			ZH: "只会修改你给出的选项，未给出的字段保持不变。",
		},
		Args: []Arg{
			{Name: "project-id", Hint: Text{EN: "from project list", ZH: "来自 project list"}, Required: true},
		},
		Flags: []Flag{
			{Name: "--name", Hint: Text{EN: "new project name, ≤80 chars", ZH: "新项目名称，≤80 字符"}, Value: "TEXT"},
			{Name: "--instructions", Hint: Text{EN: "new instructions, ≤8000 chars", ZH: "新指令提示词，≤8000 字符"}, Value: "TEXT"},
		},
		Examples: []string{
			`project edit 01H9Z… --name "Updated Title"`,
			`project edit 01H9Z… --instructions "New guidelines"`,
		},
		SeeAlso:    []string{"project show", "project list"},
		Permission: Anyone,
		Endpoints:  []string{"PATCH /api/projects/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "project id")
			if err != nil {
				return err
			}
			body := bodyBuilder{}
			body.str(rt, "name", "name")
			body.str(rt, "instructions", "instructions")
			if len(body) == 0 {
				if rt.Session.Lang == "zh" {
					return rt.Errorf("没有需要修改的内容：请至少给出一个选项")
				}
				return rt.Errorf("nothing to change: give at least one flag")
			}
			data, _, err := rt.Call(http.MethodPatch, "/api/projects/"+url.PathEscape(ref), map[string]any(body))
			if err != nil {
				return err
			}
			p := asMap(data)
			return rt.Fields([][2]string{
				{"id", asStr(p["id"])},
				{"name", asStr(p["name"])},
				{"updated_at", formatMS(p["updated_at"])},
			})
		},
	})

	registerCommand(Command{
		Name:    "project delete",
		Group:   "projects",
		Summary: Text{EN: "Delete one of your projects", ZH: "删除你的某个项目"},
		Usage:   "project delete <project-id> --yes",
		Help: Text{
			EN: "Deletes the project. Its conversations survive with their project association removed.",
			ZH: "删除该项目。其中的对话将被保留，但不再归属于任何项目。",
		},
		Args: []Arg{
			{Name: "project-id", Hint: Text{EN: "from project list", ZH: "来自 project list"}, Required: true},
		},
		Examples:    []string{"project delete 01H9Z… --yes", "project delete 01H9Z… -y"},
		SeeAlso:     []string{"project list"},
		Permission:  Anyone,
		Destructive: true,
		Endpoints:   []string{"DELETE /api/projects/{id}"},
		Run: func(_ context.Context, rt *Runtime) error {
			ref, err := requireRef(rt, "project id")
			if err != nil {
				return err
			}
			if _, _, err := rt.Call(http.MethodDelete, "/api/projects/"+url.PathEscape(ref), nil); err != nil {
				return err
			}
			if rt.Session.Lang == "zh" {
				rt.Printf("已删除。\n")
			} else {
				rt.Printf("deleted.\n")
			}
			return nil
		},
	})
}
