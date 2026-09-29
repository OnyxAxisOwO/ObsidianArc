// Package cardgrant allows administrators to grant usage reset cards to all
// registered users at once, with custom card name, quota window, and expiry.
package cardgrant

import (
	"embed"
	"io/fs"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
)

// Name is the plugin's identifier.
const Name = "cardgrant"

// Version is the plugin's release version.
const Version = "1.0.0"

//go:embed migrations/*.sql purge/*.sql
var embeddedFS embed.FS

func init() {
	plugin.Register(cardgrantPlugin{})
}

type cardgrantPlugin struct{}

func (cardgrantPlugin) Name() string { return Name }

func (cardgrantPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Version: Version,
		Title:   plugin.Text{EN: "Mass card grant", ZH: "全员发卡"},
		Description: plugin.Text{
			EN: "Grant quota reset cards to all users with custom name, expiry, and window.",
			ZH: "向所有用户批量发放用量重置卡，支持自定义卡片名称、截止日期与重置窗口。",
		},
		Author:  "Obsidian Arc",
		License: "MIT",
	}
}

func (cardgrantPlugin) Migrations() fs.FS { return sub("migrations") }

func (cardgrantPlugin) Purge() fs.FS { return sub("purge") }

func sub(dir string) fs.FS {
	out, err := fs.Sub(embeddedFS, dir)
	if err != nil {
		panic(err)
	}
	return out
}

func (cardgrantPlugin) Setup(h *plugin.Host) error {
	handlers := &adminHandlers{host: h}
	handlers.mount(h.Admin)
	return nil
}
