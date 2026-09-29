package riskcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

// Name is the plugin's identifier: the build tag is plugin_riskcontrol, and
// the browser's half lives in web/src/plugins/riskcontrol.
const Name = "riskcontrol"

// The settings keep the names they had when this was part of the core, so an
// instance that configured the service before it became a plugin keeps its
// configuration by compiling the plugin in, with nothing to migrate.
//
// The base is where the browser loads the service's SDK from and where this
// server verifies tokens; a path rather than a URL means the service is
// reverse-proxied under this instance's own domain. The site key is public —
// the browser's init() call carries it — and the secret is write-only, the
// way the Turnstile one is.
const (
	BaseURL    = "risk.base_url"
	Site       = "risk.site"
	SecretKey  = "risk.secret_key"
	AdminToken = "risk.admin_token"
	OnLogin    = "risk.on_login"
	// The registration.captcha_mode value that selects the service at
	// sign-up. One select owns which challenge a sign-up needs, so sign-up
	// has no switch of its own here.
	CaptchaMode = "risk"
)

func init() {
	// Unconfigured, and the sign-up mode does not select the service until an
	// operator points it somewhere — the same bargain the Turnstile keys
	// keep. The login switch is off even once configured: pasting
	// credentials is configuring, not yet challenging people.
	settings.Define(settings.Definition{Key: BaseURL, Plugin: Name})
	settings.Define(settings.Definition{Key: Site, Plugin: Name})
	settings.Define(settings.Definition{Key: SecretKey, Secret: true, Plugin: Name})
	settings.Define(settings.Definition{Key: AdminToken, Secret: true, Permission: "security", Plugin: Name})
	settings.Define(settings.Definition{Key: OnLogin, Default: "false", Plugin: Name})
	settings.AddCaptchaMode(Name, CaptchaMode)
	plugin.Register(riskPlugin{})
}

type riskPlugin struct{}

func (riskPlugin) Name() string { return Name }

// Version is the plugin's own, shown in its manifest and recorded when it is
// installed.
const Version = "1.1.0"

func (riskPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Version: Version,
		Title:   plugin.Text{EN: "Risk control", ZH: "超级风控"},
		Description: plugin.Text{
			EN: "A self-hosted risk-control service in front of sign-up and, optionally, sign-in: " +
				"the browser collects a token, this server has the service judge it.",
			ZH: "在注册（以及可选的登录）前接入自建风控服务：浏览器获取令牌，由本服务器交给风控服务判定。",
		},
		Author:  "Obsidian Arc",
		License: "MIT",
	}
}

func (riskPlugin) Setup(h *plugin.Host) error {
	set := h.Settings
	// Its own client: a pool per destination is what keeps a slow service
	// here from sitting in front of a Turnstile check the way it does for
	// the provider calls.
	client := &http.Client{}
	publicURL := func() string {
		if h.PublicURL == nil {
			return ""
		}
		return h.PublicURL()
	}
	// Where the token check happens. A path-only base is built against the
	// configured public URL — the same origin the OAuth callbacks resolve
	// against, and the one the visitor's browser loaded the SDK from.
	endpoint := func() string { return Endpoint(set.Get(BaseURL), publicURL()) }
	// All three credentials at once. A gate that only half of them can serve
	// would challenge a browser and then fail every token it collected, so
	// half a configuration reads as off.
	ready := func() bool {
		return set.Get(BaseURL) != "" && set.Get(Site) != "" && set.Get(SecretKey) != ""
	}
	onSignup := func() bool { return set.RegistrationCaptchaMode() == CaptchaMode }
	onLogin := func() bool { return set.Bool(OnLogin) }
	gate := func(enabled func() bool) Gate {
		return Gate{
			Client:   client,
			Enabled:  func() bool { return ready() && enabled() },
			Endpoint: endpoint,
			Site:     func() string { return set.Get(Site) },
			Secret:   func() string { return set.Get(SecretKey) },
		}
	}

	register := gate(onSignup)
	h.Auth.AddGuard(auth.GuardRegister, auth.Guard{
		Name: Name, Plugin: Name, Event: "risk_challenge",
		Check: func(ctx context.Context, req auth.GuardRequest) (auth.Verdict, error) {
			return judge(ctx, register, req, ActionRegister)
		},
	})
	login := gate(onLogin)
	h.Auth.AddGuard(auth.GuardLogin, auth.Guard{
		Name: Name, Plugin: Name, Event: "risk_challenge",
		Check: func(ctx context.Context, req auth.GuardRequest) (auth.Verdict, error) {
			return judge(ctx, login, req, ActionLogin)
		},
	})

	// Whether a browser is sent to the service at all right now: configured,
	// and one of the two doors asking for it.
	inUse := func() bool { return ready() && (onSignup() || onLogin()) }

	// Served on the terms the Turnstile key is: the address and the site key
	// are what the browser's init() call carries, and a page with no check
	// on it is not handed them. The secret stays server-side. Nothing for
	// the first account, which no challenge stands in front of.
	h.AuthHandlers.Extend(Name, func(firstAccount bool) map[string]any {
		live := !firstAccount && inUse()
		out := map[string]any{
			"base_url":  "",
			"site":      "",
			"on_signup": !firstAccount && onSignup(),
			"on_login":  !firstAccount && onLogin(),
		}
		if live {
			out["base_url"] = set.Get(BaseURL)
			out["site"] = set.Get(Site)
		}
		return out
	})

	// The service's origin, and only while a browser would actually be sent
	// there — the same rule the Turnstile exception keeps. The block above
	// hands out the base on the same terms, so the policy and the page can
	// never disagree about what is being loaded.
	h.AllowOrigin(func() string {
		if !inUse() {
			return ""
		}
		parsed, err := url.Parse(endpoint())
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return ""
		}
		return parsed.Scheme + "://" + parsed.Host
	})
	h.AllowOrigin(func() string {
		if !inUse() {
			return ""
		}
		return "blob:"
	})

	admin := newAdminHandlers(h)
	admin.mount(h.Admin)
	return nil
}

// judge turns the service's verdict into the guard's. The verdict is a band
// rather than a boolean: "block" refuses, and "challenge" — a score the
// service was not sure enough about to reject — goes through restricted at
// sign-up and unhindered at sign-in (the guard contract says Restrict means
// nothing there). Nothing distinguishes a token the service refused from one
// that never arrived; both are a failed check.
func judge(ctx context.Context, gate Gate, req auth.GuardRequest, action string) (auth.Verdict, error) {
	result, err := gate.Check(ctx, req.Token, action, req.IP)
	switch {
	case errors.Is(err, ErrFailed):
		return auth.Verdict{}, &auth.GuardRefusal{
			Reason: "风控验证未通过",
			Err: httpx.ForbiddenCode("challenge_failed",
				"The verification could not be completed. Try again."),
		}
	case errors.Is(err, ErrUnavailable):
		// Not the visitor's fault, and a different status so a monitor can
		// tell the operator's service being down from a wave of bots.
		return auth.Verdict{}, &auth.GuardRefusal{
			Reason: "风控验证未通过",
			Err: httpx.UnavailableCode("challenge_unavailable",
				"Verification is unavailable right now. Try again shortly."),
		}
	case err != nil:
		return auth.Verdict{}, err
	}
	switch result.Decision {
	case DecisionBlock:
		// A code of its own, because "the challenge failed" would invite a
		// retry the service is going to refuse again.
		return auth.Verdict{}, &auth.GuardRefusal{
			Reason: "风控判定拒绝",
			Err:    httpx.ForbiddenCode("risk_blocked", "This request was rejected by risk control."),
		}
	case DecisionChallenge:
		return auth.Verdict{Restrict: true, Reason: "风控评分落在二次验证区间"}, nil
	}
	return auth.Verdict{}, nil
}
