package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// pagedInviteRig answers the invite search from a fixed list of codes, paged
// the way the server pages it, and records every request.
type pagedInviteRig struct {
	console *Console
	calls   []string
}

// newPagedInviteRig serves codes in order and reports total as the number of
// matches. A total larger than len(codes) stands in for a search too broad to
// list: every page past the end is a full page of filler.
func newPagedInviteRig(t *testing.T, codes []string, total int) *pagedInviteRig {
	t.Helper()
	rig := &pagedInviteRig{}
	rig.console = New(Options{
		Audit: func(_ context.Context, _ AuditRecord) {},
		Dispatch: func(_ context.Context, _ user.User, method, path string, _ any) (Response, error) {
			rig.calls = append(rig.calls, method+" "+path)
			if method == http.MethodDelete {
				return Response{Status: 200, Body: []byte(`{"ok":true}`)}, nil
			}
			u, err := url.Parse(path)
			if err != nil {
				t.Fatalf("parse %q: %v", path, err)
			}
			limit, _ := strconv.Atoi(u.Query().Get("limit"))
			offset, _ := strconv.Atoi(u.Query().Get("offset"))
			page := []map[string]any{}
			for i := offset; i < offset+limit && i < total; i++ {
				code := fmt.Sprintf("FILL%04d", i)
				if i < len(codes) {
					code = codes[i]
				}
				page = append(page, map[string]any{"id": "id-" + code, "code": code})
			}
			body, err := json.Marshal(map[string]any{"codes": page, "total": total})
			if err != nil {
				t.Fatalf("marshal page: %v", err)
			}
			return Response{Status: 200, Body: body}, nil
		},
	})
	return rig
}

func (rig *pagedInviteRig) revoke(t *testing.T, line string) (Result, string) {
	t.Helper()
	rig.calls = nil
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	return runLine(rig.console, actor, line, false)
}

func (rig *pagedInviteRig) deleted() []string {
	var out []string
	for _, call := range rig.calls {
		if strings.HasPrefix(call, http.MethodDelete+" ") {
			out = append(out, call)
		}
	}
	return out
}

// The search is newest-first and an older code can sit past the first page.
// Reading only that page reported a real code as missing.
func TestInviteRevokeFindsAnExactCodePastTheFirstPage(t *testing.T) {
	codes := make([]string, 260)
	for i := range codes {
		codes[i] = fmt.Sprintf("FILL%04d", i)
	}
	codes[230] = "PARTNERX"
	rig := newPagedInviteRig(t, codes, len(codes))

	if result, out := rig.revoke(t, "invite revoke PARTNERX --yes"); !result.OK {
		t.Fatalf("the code on the second page was reported missing: %s", out)
	}
	if want := "DELETE /api/admin/invites/id-PARTNERX"; len(rig.deleted()) != 1 || rig.deleted()[0] != want {
		t.Errorf("deleted = %v, want exactly %s", rig.deleted(), want)
	}
}

// A code that no page holds is still reported missing, and nothing is revoked.
func TestInviteRevokeStillRefusesACodeThatIsNowhere(t *testing.T) {
	rig := newPagedInviteRig(t, []string{"OTHERONE"}, 1)
	result, out := rig.revoke(t, "invite revoke PARTNERX --yes")
	if result.OK {
		t.Fatalf("a missing code was accepted: %s", out)
	}
	if len(rig.deleted()) != 0 {
		t.Errorf("a missing code reached the server: %v", rig.calls)
	}
}

// A search too broad to page through is refused and the operator is asked for
// the full code, instead of the console making hundreds of requests.
func TestInviteRevokeStopsAtThePageLimitAndAsksForTheFullCode(t *testing.T) {
	rig := newPagedInviteRig(t, nil, 1000000)
	result, out := rig.revoke(t, "invite revoke A --yes")
	if result.OK {
		t.Fatalf("a search past the page limit was accepted: %s", out)
	}
	if !strings.Contains(out, "full code") {
		t.Errorf("the refusal does not ask for the full code: %q", out)
	}
	if len(rig.deleted()) != 0 {
		t.Errorf("a broad search revoked something: %v", rig.calls)
	}
	gets := 0
	for _, call := range rig.calls {
		if strings.HasPrefix(call, http.MethodGet+" ") {
			gets++
		}
	}
	if gets != inviteSearchMaxPages {
		t.Errorf("made %d search requests, want %d", gets, inviteSearchMaxPages)
	}
}
