package web

import (
	"net/http"
)

// CanvasPath is where the Canvas frame's document is served. A path of this
// server's rather than a srcdoc or a blob: a document of either kind inherits
// the page's own policy, which allows no inline script — so model-written
// JavaScript would render and never run. A response of its own carries a
// policy of its own.
const CanvasPath = "/canvas/frame"

// canvasPolicy is the whole of what a page a model wrote may do.
//
// The `sandbox` directive is the property that matters, and it is set here as
// well as on the <iframe> so that it holds even if this address is opened
// some other way — in a tab of its own, or framed by a page that forgot the
// attribute. Without allow-same-origin the document runs in an opaque origin:
// no cookie, no storage, no same-origin request to this server, and nothing
// of the page that framed it. Without allow-popups, allow-forms and
// allow-top-navigation it cannot open a window, submit anywhere, or take the
// reader away from the chat.
//
// Inline script and eval are allowed because running them is the feature;
// the sandbox is what makes that safe. connect-src 'none' and the absence of
// any network source for script, style, image or font keep the page from
// fetching anything or beaconing out what the reader types into it.
// frame-ancestors 'self' lets the chat frame it and nobody else.
const canvasPolicy = "default-src 'none'; " +
	"script-src 'unsafe-inline' 'unsafe-eval'; " +
	"style-src 'unsafe-inline'; " +
	"img-src data: blob:; " +
	"font-src data:; " +
	"media-src data: blob:; " +
	"connect-src 'none'; " +
	"form-action 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'self'; " +
	"sandbox allow-scripts"

// canvasShell is the frame's first document: a listener that waits for the
// page the chat hands it, and then becomes that page.
//
// The code arrives by postMessage rather than in the URL or the request: it
// never reaches the server, so it is never logged, and the address stays one
// fixed string that can be cached. Only a message from the window that framed
// this one is accepted — a sandboxed document has an opaque origin, so the
// parent has to post with '*', and the source check is what stands in for
// the origin check it cannot make. The document is written once; running
// another page is a fresh load of this address, never a second write into a
// document a previous page has had its hands on.
const canvasShell = `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body><script>
(function () {
  var done = false;
  window.addEventListener('message', function (event) {
    if (done || event.source !== window.parent) return;
    var data = event.data;
    if (!data || data.type !== 'arc-canvas-run' || typeof data.html !== 'string') return;
    done = true;
    document.open();
    document.write(data.html);
    document.close();
  });
  window.parent.postMessage({ type: 'arc-canvas-ready' }, '*');
})();
</script></body></html>
`

// CanvasHandler serves the frame's document while enabled says the operator
// has switched Canvas on, and a 404 otherwise — an instance that has not
// chosen the feature has no address that will run a model's code.
//
// It runs inside SecurityHeaders and replaces what that middleware set for
// every other response: the application's policy would block the very
// script this exists to run, and X-Frame-Options: DENY would stop the chat
// from framing it at all.
func CanvasHandler(enabled func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if enabled == nil || !enabled() {
			http.NotFound(w, r)
			return
		}
		header := w.Header()
		header.Set("Content-Security-Policy", canvasPolicy)
		header.Del("X-Frame-Options")
		header.Set("Content-Type", "text/html; charset=utf-8")
		header.Set("X-Content-Type-Options", "nosniff")
		// The shell is the same bytes for everyone, but the switch is not:
		// a cached copy would keep answering after an operator turned the
		// feature off.
		header.Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte(canvasShell))
	})
}
