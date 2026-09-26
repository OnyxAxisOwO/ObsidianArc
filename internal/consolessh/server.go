// Package consolessh is the SSH transport for the administrative console.
//
// It is a second door onto the same console engine the browser terminal
// talks to (internal/console), not a shell. There is no path from this
// package to os/exec anywhere: a "shell" request drives the console's own
// line-oriented Execute loop, and an "exec" request runs the one line it
// was given through Execute exactly once — never a host command. Every
// permission decision is the console engine's, re-checked against the same
// /api/admin/* routes the web UI uses; this package only carries bytes and
// keeps a person authenticated as an administrator on the other end of
// them.
package consolessh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Config is what New needs to start the listener. The zero value is not
// valid: Console and Authenticate have no safe default, so New refuses it
// rather than silently running a console nobody can reach or one that lets
// anybody in.
type Config struct {
	Addr        string // ":2222"
	HostKeyPath string // <data dir>/ssh_host_ed25519_key

	Console *console.Console

	// Authenticate verifies a password against the same store the web login
	// uses and returns the account. Whether that account may have a console
	// is Permitted's question, asked by consolessh itself and folded into the
	// same error a wrong password gets, so the prompt cannot be used to find
	// out which accounts hold it.
	Authenticate func(ctx context.Context, username, password, ip string) (user.User, error)

	// Permitted answers whether an account may use the console at all: the
	// same rule the web terminal applies, so the two doors cannot disagree
	// about who is let in. Asked at the handshake and again before every
	// command. Nil admits administrators only, which is what this transport
	// did before the terminal was offered to every account.
	Permitted func(ctx context.Context, account user.User) bool

	// Reauthorize re-reads the account behind a live session.
	//
	// A console connection outlives the moment it was authenticated, and the
	// grants it was opened with are not necessarily the grants its owner
	// still holds. The web transport never had this problem: auth.Attach
	// resolves the session cookie against the database on every request,
	// which is also how disabling an account stops it "now, not at its next
	// expiry". An SSH connection has no cookie and no per-request lookup, so
	// without this the actor is frozen at the handshake and a demoted,
	// narrowed, disabled or deleted administrator keeps everything they had
	// until they happen to disconnect.
	//
	// Called before every command. An error ends the session.
	Reauthorize func(ctx context.Context, userID string) (user.User, error)

	// SecondFactor checks the code from an authenticator app, for an account
	// that has two-step sign-in switched on. The password alone opens the
	// web sign-in only halfway for such an account, and this door must not
	// be the one that opens all the way on it.
	//
	// Asked after the password, as a keyboard-interactive prompt: the SSH
	// protocol's own "that was right, now this", which every OpenSSH client
	// understands. Nil refuses such accounts outright rather than admitting
	// them on a password.
	SecondFactor func(ctx context.Context, account user.User, code, ip string) error

	// ConnectionContext makes the context every command on one connection
	// runs under, once per connection. It is where the wiring keeps state
	// that belongs to a connection and must end with it — the visit to the
	// backoffice a code at the console's `2fa backoffice` opens — without
	// this package having to know what that state is. Nil is a bare
	// background context.
	ConnectionContext func(ctx context.Context) context.Context

	IdleTimeout time.Duration // default 30m
	MaxSessions int           // default 16, 0 = unlimited

	Logf func(ctx context.Context, msg string, args ...any)
}

// errAuthFailed is returned for every password-callback failure: wrong
// password, unknown account, disabled account and non-administrator all
// collapse to this one error so none of them is distinguishable at the SSH
// prompt.
var errAuthFailed = errors.New("consolessh: incorrect username or password")

// Server is one SSH listener serving the console. The zero value is not
// usable; construct one with New.
type Server struct {
	cfg       Config
	signer    ssh.Signer
	sshConfig *ssh.ServerConfig

	mu       sync.Mutex
	listener net.Listener
	addr     string
	closed   bool
	sessions map[*sshSession]struct{}
	// Every accepted connection, not just the ones with a live channel.
	// Sessions come and go within one connection — a one-shot exec closes
	// its channel and leaves the connection open on purpose, so a client
	// can run a second command over it — which used to leave Shutdown with
	// nothing to close and its handleConn goroutine parked on `range chans`
	// until the deadline expired.
	conns map[net.Conn]struct{}

	sem chan struct{} // nil when MaxSessions == 0 (unlimited)
	wg  sync.WaitGroup
}

// New loads or creates the host key and prepares the server, but does not
// listen yet — that is ListenAndServe's job, so a bad HostKeyPath fails at
// wiring time, in the caller's boot context, rather than surfacing later as
// a failed goroutine nobody is watching.
func New(cfg Config) (*Server, error) {
	if cfg.Console == nil {
		return nil, errors.New("consolessh: Config.Console is required")
	}
	if cfg.Authenticate == nil {
		return nil, errors.New("consolessh: Config.Authenticate is required")
	}
	if cfg.HostKeyPath == "" {
		return nil, errors.New("consolessh: Config.HostKeyPath is required")
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	if cfg.Logf == nil {
		cfg.Logf = func(context.Context, string, ...any) {}
	}

	signer, err := loadOrCreateHostKey(cfg.HostKeyPath)
	if err != nil {
		return nil, err
	}

	srv := &Server{
		cfg:      cfg,
		signer:   signer,
		sessions: make(map[*sshSession]struct{}),
	}
	if cfg.MaxSessions > 0 {
		srv.sem = make(chan struct{}, cfg.MaxSessions)
	}

	sshConfig := &ssh.ServerConfig{
		// Beyond this many failed passwords the connection is dropped; the
		// real brake on guessing is auth.Limiter, shared with the web login
		// through Authenticate, which is keyed by IP and identifier rather
		// than by connection.
		MaxAuthTries:     6,
		ServerVersion:    "SSH-2.0-ObsidianArc",
		PasswordCallback: srv.passwordCallback,
	}
	sshConfig.AddHostKey(signer)
	srv.sshConfig = sshConfig

	return srv, nil
}

// Fingerprint is the SHA256 fingerprint of the host key, exactly as an SSH
// client reports it — what `help ssh` and the startup log show so an
// administrator can verify the console before trusting it.
func (s *Server) Fingerprint() string { return fingerprint(s.signer) }

// Addr is the resolved listen address, useful when Config.Addr ends in
// ":0". Empty until ListenAndServe has bound the socket.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// ListenAndServe binds and accepts connections until Shutdown is called, at
// which point it returns nil rather than an error — mirroring
// http.Server.ListenAndServe's http.ErrServerClosed contract lets
// cmd/server/main.go treat this listener exactly like the HTTP one in its
// startup select.
func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("consolessh: listen on %s: %w", s.cfg.Addr, err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = listener.Close()
		return nil
	}
	s.listener = listener
	s.addr = listener.Addr().String()
	s.mu.Unlock()

	for {
		conn, err := listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

// Shutdown closes the listener and every live session, then waits for their
// goroutines to finish or ctx to expire — the same shape as http.Server's
// Shutdown, so main.go can hand both the same deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	listener := s.listener
	live := make([]*sshSession, 0, len(s.sessions))
	for sess := range s.sessions {
		live = append(live, sess)
	}
	conns := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	for _, sess := range live {
		sess.close()
	}
	// After the sessions, so a command still writing its last line has had
	// its context cancelled before the socket under it goes.
	for _, conn := range conns {
		_ = conn.Close()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// passwordCallback is the one way in: no "none" and no public key. Keyboard-
// interactive is offered only after a right password, and only for the code
// of an account with two-step sign-in. A successful verification's account
// travels to the connection handler through ssh.Permissions.Extensions,
// JSON-encoded, because that is the only channel the ssh package offers
// between the callback and the rest of the handshake.
func (s *Server) passwordCallback(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	ip := hostOnly(conn.RemoteAddr())
	account, err := s.cfg.Authenticate(context.Background(), conn.User(), string(password), ip)
	if err != nil {
		return nil, errAuthFailed
	}
	// Checked here, not inside Authenticate: collapsing it into the same
	// error a wrong password gets is what stops this prompt from being
	// usable to enumerate which accounts have a console.
	if !s.permitted(context.Background(), account) {
		return nil, errAuthFailed
	}

	if account.TwoFactorEnabled() {
		if s.cfg.SecondFactor == nil {
			return nil, errAuthFailed
		}
		// Partial success: the password is spent, and the only way on is
		// the code. The account travels in the closure rather than being
		// looked up again, so the prompt cannot be answered for somebody
		// other than whoever typed the password.
		return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
			KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				answers, err := challenge("", "", []string{"Verification code: "}, []bool{false})
				if err != nil || len(answers) != 1 {
					return nil, errAuthFailed
				}
				if err := s.cfg.SecondFactor(context.Background(), account, answers[0], ip); err != nil {
					return nil, errAuthFailed
				}
				return actorPermissions(account)
			},
		}}
	}
	return actorPermissions(account)
}

func actorPermissions(account user.User) (*ssh.Permissions, error) {
	encoded, err := json.Marshal(account)
	if err != nil {
		return nil, errAuthFailed
	}
	return &ssh.Permissions{Extensions: map[string]string{"actor": string(encoded)}}, nil
}

func actorFromPermissions(perm *ssh.Permissions) (user.User, bool) {
	if perm == nil {
		return user.User{}, false
	}
	raw, ok := perm.Extensions["actor"]
	if !ok {
		return user.User{}, false
	}
	var account user.User
	if err := json.Unmarshal([]byte(raw), &account); err != nil {
		return user.User{}, false
	}
	return account, true
}

func (s *Server) handleConn(conn net.Conn) {
	s.addConn(conn)
	defer s.removeConn(conn)

	// A peer that connects and then says nothing would otherwise hold a
	// goroutine and a file descriptor for as long as it liked, before
	// authenticating anything — the cheapest denial of service there is
	// against a listener on the public internet. Cleared once the handshake
	// is done, because a console session is legitimately idle for long
	// stretches and the idle timeout is what governs it from then on.
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))

	sconn, chans, reqs, err := ssh.NewServerConn(conn, s.sshConfig)
	if err != nil {
		// A failed handshake is routine on the public internet (scanners,
		// mistyped passwords) and not worth more than a debug-level trace;
		// Logf's caller decides the level.
		s.cfg.Logf(context.Background(), "consolessh: handshake failed", "remote", conn.RemoteAddr().String(), "error", err)
		return
	}
	defer sconn.Close()
	_ = conn.SetDeadline(time.Time{})
	go ssh.DiscardRequests(reqs)

	actor, ok := actorFromPermissions(sconn.Permissions)
	if !ok {
		return
	}
	ip := hostOnly(sconn.RemoteAddr())
	base := context.Background()
	if s.cfg.ConnectionContext != nil {
		base = s.cfg.ConnectionContext(base)
	}

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			// Port forwarding, X11, and anything else: this listener has
			// exactly one purpose, and accepting other channel types would
			// turn an authenticated console session into a general SSH
			// server.
			_ = newChannel.Reject(ssh.UnknownChannelType, "only a console session is available")
			continue
		}
		if !s.acquireSlot() {
			_ = newChannel.Reject(ssh.ResourceShortage, "too many concurrent console sessions")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			s.releaseSlot()
			continue
		}

		sess := newSSHSession(s, sconn, channel, actor, ip)
		sess.base = base
		s.addSession(sess)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.releaseSlot()
			defer s.removeSession(sess)
			sess.serve(requests)
		}()
	}
}

// handshakeTimeout bounds the unauthenticated part of a connection: from
// accept to a completed SSH handshake.
const handshakeTimeout = 30 * time.Second

func (s *Server) addConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns == nil {
		s.conns = make(map[net.Conn]struct{})
	}
	s.conns[conn] = struct{}{}
}

func (s *Server) removeConn(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, conn)
}

func (s *Server) acquireSlot() bool {
	if s.sem == nil {
		return true
	}
	select {
	case s.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseSlot() {
	if s.sem == nil {
		return
	}
	<-s.sem
}

func (s *Server) addSession(sess *sshSession) {
	s.mu.Lock()
	s.sessions[sess] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) removeSession(sess *sshSession) {
	s.mu.Lock()
	delete(s.sessions, sess)
	s.mu.Unlock()
}

// hostOnly strips the port from a net.Addr so the console engine and its
// audit trail record the same shape of address httpx.ClientIP produces for
// the web transport.
func hostOnly(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

// --- one session: pty/window/env negotiation, then shell or exec ---

type sshSession struct {
	server  *Server
	conn    ssh.Conn // the whole connection, not just this channel — see close's doc comment
	channel ssh.Channel
	actor   user.User
	ip      string
	// What every command on this connection runs under: shared by all the
	// connection's channels, gone when it hangs up.
	base context.Context

	mu     sync.Mutex
	width  int
	lang   string
	hasPty bool
	cancel context.CancelFunc // set while a command is running

	closeOnce sync.Once
}

func newSSHSession(server *Server, conn ssh.Conn, channel ssh.Channel, actor user.User, ip string) *sshSession {
	return &sshSession{
		server:  server,
		conn:    conn,
		channel: channel,
		actor:   actor,
		ip:      ip,
		base:    context.Background(),
		width:   100, // console.Session.Width: 0 means unknown, assume 100 — pick it up front rather than repeat the fallback at every render.
		lang:    "en",
	}
}

// close ends the session by closing the whole SSH connection, not just this
// channel. ssh.Channel.Close only sends a close message and waits for the
// peer's own close in reply before a blocked local Read unblocks — the
// mux's teardown (golang.org/x/crypto/ssh's mux.loop, on the read side)
// only EOFs every channel's buffer once the underlying net.Conn itself
// errors out. Closing the connection is what makes Shutdown's deadline and
// the idle timeout actually deterministic instead of depending on a
// possibly-gone client to cooperate.
// closeChannel ends a finished one-shot command cleanly.
//
// The output is complete and the exit status is sent, so the right thing to
// close is this channel — not the connection underneath it. Closing the
// connection is a hang-up the client has to report, which is why every
// `ssh host 'user list'` used to print "Connection closed by remote host"
// underneath its own output, on stderr, on every scripted run. The client
// hangs up by itself once the channel is done.
//
// It shares closeOnce with close, so whichever end of the session arrives
// first wins and the other becomes a no-op.
func (sess *sshSession) closeChannel() {
	sess.closeOnce.Do(func() {
		_ = sess.channel.CloseWrite()
		_ = sess.channel.Close()
	})
}

// errAccountNoLongerAdmin ends a session whose owner is no longer entitled to
// one. Deliberately one error for every reason — demoted, disabled, deleted —
// because the console has nothing useful to say about which.
var errAccountNoLongerAdmin = errors.New("consolessh: the account no longer has console access")

func (s *Server) permitted(ctx context.Context, account user.User) bool {
	if s.cfg.Permitted == nil {
		return account.IsAdmin()
	}
	return s.cfg.Permitted(ctx, account)
}

const revokedMessage = "\r\nThis account no longer has console access. Closing.\r\n"

func (sess *sshSession) close() {
	sess.closeOnce.Do(func() {
		sess.mu.Lock()
		cancel := sess.cancel
		sess.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		_ = sess.conn.Close()
	})
}

type ptyRequestPayload struct {
	Term      string
	Width     uint32
	Height    uint32
	PixWidth  uint32
	PixHeight uint32
	Modes     string
}

type windowChangePayload struct {
	Width, Height, PixWidth, PixHeight uint32
}

type envPayload struct {
	Name, Value string
}

type execPayload struct {
	Command string
}

func (sess *sshSession) serve(requests <-chan *ssh.Request) {
	// Closing the connection (not just the channel) is what guarantees the
	// background reader goroutines runInteractive/watchCancel spawn are not
	// left blocked on Read after this function returns — see sess.close's
	// doc comment for why Channel.Close alone would not do that promptly.
	defer sess.close()

	for req := range requests {
		switch req.Type {
		case "pty-req":
			var payload ptyRequestPayload
			ok := ssh.Unmarshal(req.Payload, &payload) == nil
			if ok {
				sess.mu.Lock()
				sess.hasPty = true
				if payload.Width > 0 {
					sess.width = int(payload.Width)
				}
				sess.mu.Unlock()
			}
			reply(req, ok)

		case "window-change":
			var payload windowChangePayload
			ok := ssh.Unmarshal(req.Payload, &payload) == nil
			if ok && payload.Width > 0 {
				sess.mu.Lock()
				sess.width = int(payload.Width)
				sess.mu.Unlock()
			}
			reply(req, ok)

		case "env":
			var payload envPayload
			ok := ssh.Unmarshal(req.Payload, &payload) == nil
			if ok {
				sess.applyEnv(payload.Name, payload.Value)
			}
			reply(req, ok)

		case "shell":
			reply(req, true)
			sess.runInteractive()
			return

		case "exec":
			var payload execPayload
			ok := ssh.Unmarshal(req.Payload, &payload) == nil
			reply(req, ok)
			if !ok {
				return
			}
			sess.serveExec(requests, payload.Command)
			// Deliberately not sess.close(): see closeChannel.
			sess.closeChannel()
			return

		default:
			// subsystem (sftp included), break, x11-req, and anything
			// future clients invent: this session is a console, not a
			// shell, and the only requests it understands are the ones
			// handled above. A `signal` arriving here has no command to
			// interrupt; the one that does is handled in serveExec.
			reply(req, false)
		}
	}
}

// serveExec runs one command while still answering the channel's requests.
//
// The command runs on its own goroutine rather than inline because this loop
// has to stay live underneath it: a client's interrupt may arrive as a
// `signal` request rather than as a byte on the channel, and a loop blocked
// inside the command would not read it until the command it was meant to stop
// had already finished.
func (sess *sshSession) serveExec(requests <-chan *ssh.Request, line string) {
	// A signal may arrive as soon as the exec request is acknowledged. Make
	// cancellation visible before the command goroutine starts, or that early
	// signal finds no cancel function and a long-running command continues.
	ctx, cancel := context.WithCancel(sess.base)
	sess.mu.Lock()
	sess.cancel = cancel
	sess.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sess.runExec(ctx, cancel, line)
	}()

	for {
		select {
		case <-done:
			return
		case req, open := <-requests:
			if !open {
				<-done
				return
			}
			if req.Type == "signal" {
				var payload struct{ Name string }
				if ssh.Unmarshal(req.Payload, &payload) == nil && interrupting(payload.Name) {
					sess.interrupt()
				}
			}
			reply(req, false)
		}
	}
}

// interrupting reports whether a signal name means "stop what you are doing".
// Anything else is ignored rather than guessed at: this is a console, and the
// only thing there is to signal is the command in front of it.
func interrupting(name string) bool {
	switch ssh.Signal(name) {
	case ssh.SIGINT, ssh.SIGTERM, ssh.SIGQUIT, ssh.SIGHUP:
		return true
	default:
		return false
	}
}

// interrupt cancels the running command without ending the session, which is
// what Ctrl-C means. Ending the session is close's job.
func (sess *sshSession) interrupt() {
	sess.mu.Lock()
	cancel := sess.cancel
	sess.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func reply(req *ssh.Request, ok bool) {
	if req.WantReply {
		_ = req.Reply(ok, nil)
	}
}

// zhPrefixes are LANG/LC_ALL values whose language subtag is Chinese. The
// console only distinguishes en/zh, matching Session.Lang and web/src/i18n.
func langFromEnv(value string) (string, bool) {
	v := strings.ToLower(value)
	if strings.HasPrefix(v, "zh") {
		return "zh", true
	}
	if strings.HasPrefix(v, "en") {
		return "en", true
	}
	return "", false
}

func (sess *sshSession) applyEnv(name, value string) {
	if name != "LANG" && name != "LC_ALL" {
		return
	}
	lang, ok := langFromEnv(value)
	if !ok {
		return
	}
	sess.mu.Lock()
	sess.lang = lang
	sess.mu.Unlock()
}

// currentSession is snapshot with the account re-read first. Every command
// goes through this rather than snapshot, so a grant revoked while somebody
// sits at a prompt takes effect on their next command instead of whenever
// they get round to closing the connection.
//
// A failure ends the session: the account is gone, disabled, or no longer an
// administrator, and none of those should keep a console open.
func (sess *sshSession) currentSession(ctx context.Context, transport string) (*console.Session, error) {
	reauthorize := sess.server.cfg.Reauthorize
	if reauthorize == nil {
		return sess.snapshot(transport), nil
	}

	account, err := reauthorize(ctx, sess.actorID())
	if err != nil {
		return nil, err
	}
	if !account.IsActive() || !sess.server.permitted(ctx, account) {
		return nil, errAccountNoLongerAdmin
	}

	sess.mu.Lock()
	sess.actor = account
	sess.mu.Unlock()
	return sess.snapshot(transport), nil
}

func (sess *sshSession) actorID() string {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.actor.ID
}

func (sess *sshSession) snapshot(transport string) *console.Session {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return &console.Session{
		Actor:     sess.actor,
		Transport: transport,
		IP:        sess.ip,
		Width:     sess.width,
		Colour:    sess.hasPty,
		Lang:      sess.lang,
	}
}

// runExec is `ssh admin@host 'user list --json'`: one line, no pty, the
// channel's own exit status carries the verdict so `ssh ... | jq` and
// similar scripting works. Never anything but console.Console.Execute —
// this is the one function in the package that could be mistaken for a
// shell, and it is not one.
func (sess *sshSession) runExec(ctx context.Context, cancel context.CancelFunc, line string) {
	defer cancel()

	consoleSession, err := sess.currentSession(ctx, "ssh")
	if err != nil {
		_, _ = sess.channel.Write([]byte(revokedMessage))
		_, _ = sess.channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{1}))
		return
	}
	consoleSession.Colour = false

	go sess.watchCancel(ctx, cancel)

	result := sess.server.cfg.Console.Execute(ctx, consoleSession, sess.channel, line)

	status := uint32(0)
	if !result.OK {
		status = 1
	}
	_, _ = sess.channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
}

// runInteractive is the pty path: banner, prompt, line editor, until `exit`,
// `quit`, Ctrl-D on an empty line, or the idle timeout closes it.
//
// One goroutine owns every read of the channel, whether the byte it just
// read is an edit keystroke or a Ctrl-C that should cancel a running
// command. A second reader watching for Ctrl-C independently would race
// this one for bytes already sitting in the line editor's buffered reader
// — see lineEditor.Start's doc comment — so instead this loop asks the
// editor for one rune at a time and decides, based on whether a command is
// currently running, whether that rune is line-editing input or a cancel
// signal.
func (sess *sshSession) runInteractive() {
	engine := sess.server.cfg.Console
	_, _ = sess.channel.Write([]byte(engine.Banner(sess.snapshot("ssh"))))

	complete := func(line string, pos int) (int, []editorCompletion) {
		c := engine.Complete(context.Background(), sess.snapshot("ssh"), line, pos)
		items := make([]editorCompletion, len(c.Items))
		for i, item := range c.Items {
			items[i] = editorCompletion{Value: item.Value, Label: item.Label}
		}
		return c.From, items
	}
	editor := newLineEditor(sess.channel, sess.channel, "> ", complete, newHistory())
	editor.Start()

	type readEvent struct {
		r   rune
		err error
	}
	reads := make(chan readEvent)
	go func() {
		for {
			r, err := editor.NextRune()
			reads <- readEvent{r, err}
			if err != nil {
				return
			}
		}
	}()

	type execEvent struct{ result console.Result }
	results := make(chan execEvent, 1)

	var cancel context.CancelFunc
	running := false
	idle := sess.server.cfg.IdleTimeout
	idleTimer := time.NewTimer(idle)
	defer idleTimer.Stop()

	for {
		select {
		case ev := <-reads:
			if !idleTimer.Stop() {
				<-idleTimer.C
			}
			idleTimer.Reset(idle)

			if ev.err != nil {
				// io.EOF (Ctrl-D on an empty line) or the channel closing
				// underneath the reader: either way, the session is over.
				if cancel != nil {
					cancel()
				}
				return
			}

			if running {
				// A command is running: the editor is not consuming input,
				// so the only rune worth acting on is Ctrl-C, which cancels
				// that command without ending the session. Everything else
				// is discarded rather than queued, matching a real shell
				// ignoring keystrokes typed while a foreground job is busy.
				if ev.r == 0x03 && cancel != nil {
					cancel()
				}
				continue
			}

			line, done, eof := editor.Feed(ev.r)
			if eof {
				return
			}
			if !done {
				continue
			}
			if strings.TrimSpace(line) == "" {
				editor.Start()
				continue
			}

			runCtx, runCancel := context.WithCancel(sess.base)
			cancel = runCancel
			running = true
			go func(line string) {
				current, authErr := sess.currentSession(runCtx, "ssh")
				if authErr != nil {
					_, _ = sess.channel.Write([]byte(revokedMessage))
					results <- execEvent{result: console.Result{Exit: true}}
					return
				}
				result := engine.Execute(runCtx, current, sess.channel, line)
				results <- execEvent{result}
			}(line)

		case ev := <-results:
			running = false
			if cancel != nil {
				cancel()
				cancel = nil
			}
			if ev.result.Exit {
				return
			}
			editor.Start()

		case <-idleTimer.C:
			_, _ = sess.channel.Write([]byte("\r\nidle timeout; closing.\r\n"))
			if cancel != nil {
				cancel()
			}
			return
		}
	}
}

// watchCancel watches a one-shot command's channel for an interrupt. The
// exec path never constructs a lineEditor — there is nothing to edit in a
// one-shot command — so there is only the one reader here and no race to
// avoid.
//
// Only Ctrl-C cancels. The end of the client's input does not, and that
// distinction is the whole reason this function has a comment: `ssh host
// 'user list'` sends no input at all, so the first thing this read returns
// is io.EOF, immediately, before the command has done anything. Treating
// that as an interrupt cancelled the request context of every scripted
// command — which surfaced as "context canceled" from whatever query the
// command had just reached, and never once from the browser, because the
// web transport has no stdin to end.
//
// A client that actually goes away is a different event and is already
// handled: sshSession.close cancels this same context when the connection
// tears down.
func (sess *sshSession) watchCancel(ctx context.Context, cancel context.CancelFunc) {
	buf := make([]byte, 1)
	for {
		n, err := sess.channel.Read(buf)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			return
		}
		if n > 0 && buf[0] == 0x03 {
			cancel()
			return
		}
	}
}
