package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/invite"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/oauth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
)

// What attaching a package does: what a compiled-in plugin's Setup does with
// the extension points, driven by the package's manifest instead of by Go
// code, and reversible. Each point is asked to replace whatever this plugin
// put there before, in one step, so an update of a plugin that stands in
// front of sign-up never leaves the door open between the old check leaving
// and the new one arriving.

// AttachConsole hands the manager the console the packages' commands are
// added to. The console is built after the plugins are set up, so it arrives
// on its own.
func (m *Manager) AttachConsole(c *console.Console) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.console = c
	for _, l := range *m.pkgs.Load() {
		if l.faulted() != "" {
			continue
		}
		if err := c.AddCommands(l.name, m.commandsOf(l)); err != nil {
			l.fault.Store(ptr("console commands: " + err.Error()))
		}
	}
}

func ptr[T any](v T) *T { return &v }

// attach makes every seam of l's manifest live behind the plugin's gate. It
// changes nothing on failure — what was done is undone — so the caller only
// has to say what it was doing when it goes wrong. Without a host only the
// console commands can be attached; Attach calls it again when the host
// arrives and the rest follow.
func (m *Manager) attach(l *loaded) (err error) {
	h := m.host
	if h == nil {
		// detach takes console commands away with or without a host, so an update
		// or a restore that runs while there is none must put them back the same way.
		if m.console != nil {
			return m.console.AddCommands(l.name, m.commandsOf(l))
		}
		return nil
	}
	man := l.pkg.Manifest
	name := l.name

	var undo []func()
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
	}()

	if err = m.settings.AddPluginDefinitions(name, definitionsOf(man), man.CaptchaModes); err != nil {
		return err
	}
	undo = append(undo, func() { m.settings.RemovePluginDefinitions(name) })

	if err = m.users.AddPluginFields(name, fieldsOf(man)); err != nil {
		return err
	}
	undo = append(undo, func() { m.users.RemovePluginFields(name) })

	for _, rule := range man.FieldRules {
		setting := rule.Setting
		h.Auth.SetFieldRule(rule.Field, func() string { return m.settings.Get(setting) })
		field := rule.Field
		undo = append(undo, func() { h.Auth.RemoveFieldRule(field) })
	}

	for _, b := range man.OAuthBindings {
		re := arcx.MustWhole(b.Pattern)
		// Its own binding first: an update replaces it. An install has already
		// refused a field another package holds (checkPackage), so what this
		// clears is this package's own binding from an earlier version.
		h.OAuth.UnbindSubject(b.Field)
		if err = h.OAuth.TryBindSubject(oauth.SubjectBinding{Provider: b.Provider, Field: b.Field, Matches: re.MatchString}); err != nil {
			return err
		}
		field := b.Field
		undo = append(undo, func() { h.OAuth.UnbindSubject(field) })
	}

	h.Auth.ReplaceGuards(name, m.guardsOf(l))
	undo = append(undo, func() { h.Auth.RemoveGuards(name) })

	h.AuthHandlers.Extend(name, m.siteBlock(l))
	undo = append(undo, func() { h.AuthHandlers.Unextend(name) })

	// Removed whether or not the new version declares the hook: a decorator an
	// update dropped would keep calling the old backend, which closeLater closes,
	// and every invitee page would then fail until the next restart.
	h.InviteHandlers.RemoveDecorators(name)
	if l.has(arcx.HookDecorateInvitees) {
		h.InviteHandlers.DecorateInvitees(name, m.decorator(l))
		undo = append(undo, func() { h.InviteHandlers.RemoveDecorators(name) })
	}

	// An empty list is still a list: AddCommands replaces what the plugin
	// registered before, so a command an update no longer declares goes away.
	if m.console != nil {
		if err = m.console.AddCommands(name, m.commandsOf(l)); err != nil {
			return err
		}
		undo = append(undo, func() { m.console.RemoveCommands(name) })
	}

	if err = m.rebuildRouter(); err != nil {
		return err
	}
	return nil
}

// dropRetiredSeams takes away what an update's old version attached and the
// new one no longer names. attach replaces what the new manifest names, so an
// OAuth binding or field rule it dropped would stay live until the next
// restart. An uninstall only walks the installed manifest, so it would not
// take those away either: the stale binding would outlive the plugin.
func (m *Manager) dropRetiredSeams(old, next *arcx.Manifest) {
	h := m.host
	if h == nil {
		return
	}
	bound := map[string]bool{}
	for _, b := range next.OAuthBindings {
		bound[b.Field] = true
	}
	for _, b := range old.OAuthBindings {
		if !bound[b.Field] {
			h.OAuth.UnbindSubject(b.Field)
		}
	}
	ruled := map[string]bool{}
	for _, r := range next.FieldRules {
		ruled[r.Field] = true
	}
	for _, r := range old.FieldRules {
		if !ruled[r.Field] {
			h.Auth.RemoveFieldRule(r.Field)
		}
	}
}

// detach takes everything l attached away. It is what an uninstall and a
// failed update do, and it never fails: there is nothing to say no to.
func (m *Manager) detach(l *loaded) {
	// Console commands are added by AttachConsole whether or not a host is
	// there, so they come off on the same terms; otherwise a package removed
	// before the host arrives keeps its commands and the backend they close over.
	if m.console != nil {
		m.console.RemoveCommands(l.name)
	}
	h := m.host
	if h != nil {
		man := l.pkg.Manifest
		m.settings.RemovePluginDefinitions(l.name)
		m.users.RemovePluginFields(l.name)
		for _, rule := range man.FieldRules {
			h.Auth.RemoveFieldRule(rule.Field)
		}
		for _, b := range man.OAuthBindings {
			h.OAuth.UnbindSubject(b.Field)
		}
		h.Auth.RemoveGuards(l.name)
		h.AuthHandlers.Unextend(l.name)
		h.InviteHandlers.RemoveDecorators(l.name)
	}
}

// detachPackage is detach for a package being removed: it also leaves the
// list of packages, closes the backend and takes its routes away.
func (m *Manager) detachPackage(l *loaded) {
	m.detach(l)
	m.swapPackages(func(pkgs map[string]*loaded) { delete(pkgs, l.name) })
	_ = m.rebuildRouter()
	closeLater(l)
}

// closeGrace is how long a backend that has been replaced or removed is kept
// running for the calls already inside it. A call is over in at most the
// engine's call timeout, and closing under one would fail a request that had
// done nothing wrong.
var closeGrace = 30 * time.Second

func closeLater(l *loaded) {
	if l == nil || l.backend == nil {
		return
	}
	time.AfterFunc(closeGrace, l.backend.Close)
}

// swapPackages replaces the map of packages with a copy that fn has edited.
func (m *Manager) swapPackages(fn func(map[string]*loaded)) {
	next := map[string]*loaded{}
	for k, v := range *m.pkgs.Load() {
		next[k] = v
	}
	fn(next)
	m.pkgs.Store(&next)
}

// guardsOf is the guards the manifest lists, by the door each stands in front
// of, each asking the backend.
func (m *Manager) guardsOf(l *loaded) map[string][]auth.Guard {
	out := map[string][]auth.Guard{}
	for _, g := range l.pkg.Manifest.Guards {
		g := g
		out[g.Action] = append(out[g.Action], auth.Guard{
			Name: g.Name, Plugin: l.name, Event: g.Event,
			Check: func(ctx context.Context, req auth.GuardRequest) (auth.Verdict, error) {
				return m.judge(ctx, l, g.Name, req)
			},
		})
	}
	return out
}

// refusingGuards is the guards the manifests list, each refusing. They stand in
// for guards that could not be put back: a door whose check is missing is open,
// so the check stays and refuses, as a backend that cannot answer does.
func refusingGuards(name string, manifests ...*arcx.Manifest) map[string][]auth.Guard {
	out := map[string][]auth.Guard{}
	for _, man := range manifests {
		for _, g := range man.Guards {
			out[g.Action] = append(out[g.Action], auth.Guard{
				Name: g.Name, Plugin: name, Event: g.Event,
				Check: func(context.Context, auth.GuardRequest) (auth.Verdict, error) {
					return auth.Verdict{}, unavailableGuard(errPluginFault)
				},
			})
		}
	}
	return out
}

// judge asks the backend to judge a sign-up or a sign-in and turns its answer
// into the guard's. A backend that cannot answer refuses: a check that is
// down must not become a door that is open.
func (m *Manager) judge(ctx context.Context, l *loaded, name string, req auth.GuardRequest) (auth.Verdict, error) {
	var out guardAnswer
	err := m.invoke(ctx, l, wasm.CallInfo{IP: req.IP}, "guard", map[string]any{
		"name": name, "action": req.Action, "token": req.Token, "ip": req.IP, "username": req.Username,
	}, &out, nil)
	if err != nil {
		var ge *wasm.GuestError
		if errors.As(err, &ge) && deliberate(ge) {
			return auth.Verdict{}, &auth.GuardRefusal{Err: guestHTTPError(ge), Reason: ge.Code}
		}
		return auth.Verdict{}, unavailableGuard(err)
	}
	return out.verdict()
}

// guardAnswer is what a guard's backend says about a visitor.
type guardAnswer struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// verdict is the guard's answer to the visitor. Only an explicit allow lets
// the visitor through: an empty or unrecognised verdict is an answer nobody
// meant, and the zero auth.Verdict is unrestricted, so letting it fall
// through would open the door for a backend that spoke a different dialect.
func (a guardAnswer) verdict() (auth.Verdict, error) {
	switch a.Verdict {
	case "allow":
		return auth.Verdict{}, nil
	case "refuse":
		// Any error status: a check that is down is told apart from a visitor
		// who failed it by a 503, which a monitor can count.
		status := a.Status
		if status < 400 || status > 599 {
			status = 403
		}
		return auth.Verdict{}, &auth.GuardRefusal{
			Err:    &httpx.Error{Status: status, Code: a.Code, Message: a.Message},
			Reason: a.Reason,
		}
	case "restrict":
		return auth.Verdict{Restrict: true, Reason: a.Reason}, nil
	}
	return auth.Verdict{}, unavailableGuard(fmt.Errorf("guard answered %q", a.Verdict))
}

// unavailableGuard is the refusal a check that could not answer gets. The
// cause is kept for the log; the visitor is told only that verification is
// down.
func unavailableGuard(cause error) error {
	return &auth.GuardRefusal{
		Err:    httpx.UnavailableCode("plugin_unavailable", "Verification is unavailable right now. Try again shortly.").WithCause(cause),
		Reason: "plugin unavailable",
	}
}

// siteBlock is the plugin's part of /api/site: what its backend last said for
// this kind of visitor, and where the browser fetches its module from.
func (m *Manager) siteBlock(l *loaded) func(first bool) map[string]any {
	return func(first bool) map[string]any {
		block := map[string]any{}
		if d := l.described.Load(); d != nil {
			src := d.Site
			if first {
				src = d.First
			}
			for k, v := range src {
				block[k] = v
			}
		}
		if url := l.uiURL(); url != "" {
			block["_ui"] = url
		}
		return block
	}
}

// decorator is the plugin's hand in an inviter's own list.
func (m *Manager) decorator(l *loaded) invite.InviteeDecorator {
	return func(ctx context.Context, inviterID string, invitees []invite.Invitee) ([]invite.Invitee, error) {
		type row struct {
			UserID string         `json:"user_id"`
			Entry  map[string]any `json:"entry"`
		}
		in := make([]row, len(invitees))
		for i, invitee := range invitees {
			in[i] = row{UserID: invitee.UserID, Entry: invitee.Entry}
		}
		var out struct {
			Invitees []row `json:"invitees"`
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := m.invoke(ctx, l, wasm.CallInfo{}, "decorate_invitees",
			map[string]any{"inviter_id": inviterID, "invitees": in}, &out, nil)
		if err != nil {
			// The invite list skips a decorator that fails and keeps the rows it had,
			// and logs the failure once, so a backend that is down shows the list as
			// it is. A guard fails closed because it decides who gets in; this
			// decides nothing.
			return nil, err
		}
		result := make([]invite.Invitee, len(out.Invitees))
		for i, r := range out.Invitees {
			if r.Entry == nil {
				r.Entry = map[string]any{}
			}
			result[i] = invite.Invitee{UserID: r.UserID, Entry: r.Entry}
		}
		return result, nil
	}
}

// commandsOf is the console commands the manifest lists, each running the
// backend.
func (m *Manager) commandsOf(l *loaded) []console.Command {
	var out []console.Command
	for _, c := range l.pkg.Manifest.Console {
		c := c
		cmd := console.Command{
			Name: c.Name, Group: c.Group, Usage: c.Usage,
			Summary: consoleText(c.Summary), Help: consoleText(c.Help),
			Examples: c.Examples, SeeAlso: c.SeeAlso, Permission: c.Permission,
			Destructive: c.Destructive, Endpoints: c.Endpoints, Plugin: l.name,
		}
		for _, a := range c.Args {
			cmd.Args = append(cmd.Args, console.Arg{Name: a.Name, Hint: consoleText(a.Hint), Required: a.Required, Rest: a.Rest, Sensitive: a.Sensitive})
		}
		for _, f := range c.Flags {
			cmd.Flags = append(cmd.Flags, console.Flag{Name: f.Name, Hint: consoleText(f.Hint), Value: f.Value, Sensitive: f.Sensitive})
		}
		// A command with no grant named was open to every administrator. Its
		// backend runs with the package's own host permissions and cannot tell
		// which administrator asked, so an unnamed grant means the widest one a
		// terminal can hold.
		if cmd.Permission == "" {
			cmd.Permission = "super_admin"
		}
		cmd.Run = func(ctx context.Context, rt *console.Runtime) error { return m.runCommand(ctx, l, c, rt) }
		out = append(out, cmd)
	}
	return out
}

func consoleText(t arcx.Text) console.Text { return console.Text{EN: t.EN, ZH: zhOr(t)} }

// runCommand runs a console command on the backend and draws what it printed.
func (m *Manager) runCommand(ctx context.Context, l *loaded, c arcx.ConsoleCommand, rt *console.Runtime) error {
	flags := map[string]string{}
	for _, f := range c.Flags {
		name := strings.TrimLeft(f.Name, "-")
		if rt.Present(name) {
			flags[name] = rt.String(name)
		}
	}
	arg := map[string]any{
		"command": c.Name, "args": rt.Args(), "flags": flags,
		"json": rt.Session.JSON || rt.Present("json"), "yes": rt.Confirmed(),
	}
	var out struct {
		Out []struct {
			Kind    string     `json:"kind"`
			Text    string     `json:"text"`
			Headers []string   `json:"headers"`
			Rows    [][]string `json:"rows"`
		} `json:"out"`
	}
	info := wasm.CallInfo{Lang: rt.Session.Lang, IP: rt.Session.IP, Actor: actorOf(rt.Session.Actor)}
	st := &callState{console: rt}
	err := m.invoke(ctx, l, info, "console", arg, &out, st)
	if err != nil {
		return commandFailure(err, st.consoleErrs)
	}
	for _, line := range out.Out {
		switch line.Kind {
		case "table":
			headers := make([]string, len(line.Headers))
			for i, h := range line.Headers {
				headers[i] = plainText(h)
			}
			rows := make([][]string, len(line.Rows))
			for i, row := range line.Rows {
				rows[i] = make([]string, len(row))
				for j, cell := range row {
					rows[i][j] = plainText(cell)
				}
			}
			if err := rt.Table(headers, rows); err != nil {
				return err
			}
		default:
			rt.Printf("%s", plainText(line.Text))
		}
	}
	return nil
}

// commandFailure is what the console draws for a command whose backend
// failed. A deliberate refusal is shown as written. Anything else is the
// backend's own failure, whose text may name a credential it was handed, so
// it goes to the log and the operator sees a fixed sentence, as a guard's
// visitor does.
func commandFailure(err error, consoleErrs map[string]error) error {
	var ge *wasm.GuestError
	if errors.As(err, &ge) && deliberate(ge) {
		// A command that passed a refused Call on: what the console draws is
		// the endpoint's own error, code and all, as it is for a command that
		// was compiled in. The error is looked up by the message the backend
		// returned, so a command that made another refused call after this one
		// still gets this one's sentence.
		if ge.Code == "console" {
			if refused, ok := consoleErrs[ge.Message]; ok {
				return refused
			}
		}
		return errors.New(ge.Message)
	}
	slog.Error("plugin: a console command failed", "error", err)
	return errors.New("the plugin failed; the server log has the details")
}

// plainText drops the control characters a terminal would obey: the escape
// sequences that move the cursor, retitle the window, or paint over what was
// printed above. What a backend prints is often something a visitor typed (a
// name, a note), and the operator's console must show it, not act on it.
// Newline and tab are layout, and stay.
func plainText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}
