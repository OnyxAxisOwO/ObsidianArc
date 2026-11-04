package console

// Spec is what GET /api/console/spec answers: everything the web
// terminal needs to render help and complete locally, computed for one
// actor — a command they may not run is never in Commands, exactly like
// help and Complete.
type Spec struct {
	Version  string        `json:"version"`
	You      SpecYou       `json:"you"`
	SSH      SpecSSH       `json:"ssh"`
	Banner   string        `json:"banner"`
	Commands []SpecCommand `json:"commands"`
}

type SpecYou struct {
	Username    string   `json:"username"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

type SpecSSH struct {
	Enabled     bool   `json:"enabled"`
	Addr        string `json:"addr"`
	Fingerprint string `json:"fingerprint"`
}

type SpecCommand struct {
	Name        string     `json:"name"`
	Group       string     `json:"group"`
	Summary     string     `json:"summary"`
	Usage       string     `json:"usage"`
	Permission  string     `json:"permission"`
	Destructive bool       `json:"destructive"`
	Flags       []SpecFlag `json:"flags"`
	Args        []SpecArg  `json:"args"`
	Examples    []string   `json:"examples"`
	Endpoints   []string   `json:"endpoints"`
}

type SpecFlag struct {
	Name  string `json:"name"`
	Hint  string `json:"hint"`
	Value string `json:"value,omitempty"`
}

type SpecArg struct {
	Name     string `json:"name"`
	Hint     string `json:"hint"`
	Required bool   `json:"required"`
}

// Spec resolves the whole registry for s.Actor and s.Lang.
func (c *Console) Spec(s *Session) Spec {
	visible := c.reg.visible(s.Actor)
	commands := make([]SpecCommand, 0, len(visible))
	for _, cmd := range visible {
		commands = append(commands, toSpecCommand(cmd, s.Lang))
	}

	permissions := append([]string(nil), s.Actor.AdminPermissions...)
	if permissions == nil {
		permissions = []string{}
	}

	version, _ := c.visibleVersion(s.Actor)
	return Spec{
		Version: version,
		You: SpecYou{
			Username:    s.Actor.Username,
			Role:        string(s.Actor.Role),
			Permissions: permissions,
		},
		SSH: SpecSSH{
			Enabled:     c.opts.SSH.Enabled,
			Addr:        c.opts.SSH.Addr,
			Fingerprint: c.opts.SSH.Fingerprint,
		},
		Banner:   c.Banner(s),
		Commands: commands,
	}
}

func toSpecCommand(cmd *Command, lang string) SpecCommand {
	flags := make([]SpecFlag, 0, len(cmd.Flags))
	for _, f := range cmd.Flags {
		flags = append(flags, SpecFlag{Name: f.Name, Hint: f.Hint.For(lang), Value: f.Value})
	}
	args := make([]SpecArg, 0, len(cmd.Args))
	for _, a := range cmd.Args {
		args = append(args, SpecArg{Name: a.Name, Hint: a.Hint.For(lang), Required: a.Required})
	}
	examples := cmd.Examples
	if examples == nil {
		examples = []string{}
	}
	endpoints := cmd.Endpoints
	if endpoints == nil {
		endpoints = []string{}
	}

	return SpecCommand{
		Name:        cmd.Name,
		Group:       cmd.Group,
		Summary:     cmd.Summary.For(lang),
		Usage:       cmd.Usage,
		Permission:  cmd.Permission,
		Destructive: cmd.Destructive,
		Flags:       flags,
		Args:        args,
		Examples:    examples,
		Endpoints:   endpoints,
	}
}
