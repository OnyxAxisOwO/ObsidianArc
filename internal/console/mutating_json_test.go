package console

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// newStubRig is a console whose every request gets the same status and body
// from respond, so a test can say what the server answered and check only
// what the console printed about it.
func newStubRig(respond func(method, path string) (int, string)) *flagRig {
	rig := &flagRig{bodies: map[string]any{}}
	rig.console = New(Options{
		Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (Response, error) {
			rig.calls = append(rig.calls, method+" "+path)
			rig.bodies[method+" "+path] = body
			status, out := respond(method, path)
			return Response{Status: status, Body: []byte(out)}, nil
		},
	})
	return rig
}

// Under --json every command must print one JSON document. A change that
// printed a sentence instead (the way the delete and grant commands did) hands
// a script text it cannot parse, even though the change itself went through.
func TestMutatingCommandsPrintOneJSONDocumentUnderJSON(t *testing.T) {
	rig := newStubRig(func(string, string) (int, string) { return 200, `{"ok":true}` })
	providerID, modelID, otherModelID := id.New(), id.New(), id.New()
	appID, noticeID, codeID := id.New(), id.New(), id.New()
	feedbackID, replyID, groupID, userID, grantID := id.New(), id.New(), id.New(), id.New(), id.New()
	keyID := id.New()
	lines := []string{
		"provider delete " + providerID + " --yes",
		"model delete " + modelID + " --yes",
		"model order " + modelID + " " + otherModelID,
		"app delete " + appID + " --yes",
		"notice delete " + noticeID + " --yes",
		"code delete " + codeID + " --yes",
		"quota delete global --yes",
		"feedback delete " + feedbackID + " --yes",
		"feedback reply delete " + feedbackID + " " + replyID + " --yes",
		"group delete " + groupID + " --yes",
		"group assign " + groupID + " --users " + userID,
		"bonus revoke " + grantID + " --yes",
		"checkin now",
		"checkin claim w1",
		"checkin",
		"user delete " + userID + " --yes",
		"user passwd " + userID + " --password a-good-password --yes",
		"user passwd " + userID + " --generate --yes",
		"user key-revoke " + userID + " " + keyID + " --yes",
		"login-bg set landscape_light iVBORw0KGgo=",
		"login-bg clear landscape_light --yes",
		"logo set iVBORw0KGgo=",
		"logo clear --yes",
	}
	for _, line := range lines {
		result, out := rig.runJSON(line + " --json")
		if !result.OK {
			t.Errorf("%s failed: %s", line, out)
			continue
		}
		if !json.Valid([]byte(strings.TrimSpace(out))) {
			t.Errorf("%s did not print one JSON document under --json:\n%s", line, out)
		}
	}
}

// The probe's error frame is the only sign that a run stopped short. Its exit
// status has to say so, and a clean run's frames must come back as one array.
func TestAHealthProbeErrorFrameFailsTheCommand(t *testing.T) {
	rig := newStubRig(func(method, path string) (int, string) {
		return 200, "event: progress\ndata: {\"completed\":1}\n\nevent: error\ndata: {\"code\":\"record_failed\"}\n\n"
	})
	result, out := rig.run("health probe")
	if result.OK {
		t.Errorf("a probe that reported an error exited as success: %s", out)
	}
	if !strings.Contains(out, "stopped with an error") {
		t.Errorf("the failure is not named in the output: %q", out)
	}
}

func TestHealthProbeUnderJSONIsOneArrayOfFrames(t *testing.T) {
	rig := newStubRig(func(method, path string) (int, string) {
		return 200, "event: progress\ndata: {\"completed\":1}\n\nevent: done\ndata: {\"completed\":1}\n\n"
	})
	result, out := rig.runJSON("health probe --json")
	if !result.OK {
		t.Fatalf("health probe --json failed: %s", out)
	}
	var frames []map[string]any
	if err := json.Unmarshal([]byte(out), &frames); err != nil {
		t.Fatalf("health probe --json is not one JSON array: %v\n%s", err, out)
	}
	if len(frames) != 2 || frames[0]["event"] != "progress" || frames[1]["event"] != "done" {
		t.Errorf("frames = %v, want progress then done", frames)
	}
	if data, ok := frames[1]["data"].(map[string]any); !ok || data["completed"] != float64(1) {
		t.Errorf("done frame data = %#v, want the structured counts", frames[1]["data"])
	}
}

// The generated password is shown once and is not stored, so under --json it
// has to be in the document: the POST that set it answers without it.
func TestPasswdGenerateUnderJSONCarriesThePassword(t *testing.T) {
	rig := newStubRig(func(string, string) (int, string) { return 200, `{"ok":true}` })
	userID := id.New()
	result, out := rig.runJSON("user passwd " + userID + " --generate --yes --json")
	if !result.OK {
		t.Fatalf("user passwd --generate --json failed: %s", out)
	}
	if password, _ := decodeObject(t, out)["password"].(string); password == "" {
		t.Errorf("the generated password is missing from the JSON document:\n%s", out)
	}
}

// An error frame with an empty payload is still a failed run. The exit status
// must not depend on the frame carrying text.
func TestAHealthProbeEmptyErrorFrameStillFailsTheCommand(t *testing.T) {
	rig := newStubRig(func(string, string) (int, string) {
		return 200, "event: progress\ndata: {\"completed\":1}\n\nevent: error\n\n"
	})
	if result, out := rig.run("health probe"); result.OK {
		t.Errorf("a probe whose error frame was empty exited as success: %s", out)
	}
	if result, out := rig.runJSON("health probe --json"); result.OK {
		t.Errorf("under --json a probe whose error frame was empty exited as success: %s", out)
	}
}
