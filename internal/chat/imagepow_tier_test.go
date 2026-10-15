package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
)

// The challenge endpoint raises the difficulty for an address that has been
// busy, and a picture is paid for with the same proof of work. Work done at
// the base tier must stop buying pictures once the tier has risen, or the
// price of a picture is a tenth of what the operator configured.
func TestImagePictureRefusesBaseTierWorkOnceTheTierHasRisen(t *testing.T) {
	lab := newImageLab(t, 1)
	mgr := pow.NewManager([]byte("a-test-key-for-the-lab"), nil)
	lab.handlers.ImagePoW = mgr
	lab.handlers.ImagePoWEnabled = func() bool { return true }
	lab.handlers.ImagePoWTier = func(string) int64 { return 5000 }
	lab.fixture.upstream.reply(`{"created":1,"data":[{"b64_json":"` + generatedPNG + `"}]}`)

	base := solveAtTier(t, mgr, 1000)
	payload, _ := json.Marshal(base)
	recorder := lab.generate(t, lab.askBody()+`,"pow":`+string(payload)+`}`)
	if recorder.Code == http.StatusOK {
		t.Fatalf("base-tier work bought a picture after the tier rose: %s", recorder.Body.String())
	}
	// A refusal for any other reason (a malformed body, a verification
	// failure) would also be non-200, so the code is what shows that the
	// tier check is what refused the work.
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &envelope) != nil || envelope.Error.Code != "pow_expired" {
		t.Fatalf("base-tier work was refused as %q, want pow_expired: %s", envelope.Error.Code, recorder.Body.String())
	}
	if lab.paintedOnce() {
		t.Fatal("the provider was called for a proof of work the tier refused")
	}

	current := solveAtTier(t, mgr, 5000)
	payload, _ = json.Marshal(current)
	if code := lab.generate(t, lab.askBody()+`,"pow":`+string(payload)+`}`).Code; code != http.StatusOK {
		t.Fatalf("work at the current tier got %d, want 200", code)
	}
}

// solveAtTier is solve with the difficulty chosen by the caller, so one test
// can hold both the base-tier and the current-tier solution.
func solveAtTier(t *testing.T, mgr *pow.Manager, maxNumber int64) *pow.Solution {
	t.Helper()
	c, err := mgr.Issue(maxNumber)
	if err != nil {
		t.Fatal(err)
	}
	for n := int64(0); n <= c.MaxNumber; n++ {
		sum := sha256.Sum256([]byte(c.Salt + strconv.FormatInt(n, 10)))
		if hex.EncodeToString(sum[:]) == c.Challenge {
			return &pow.Solution{
				Challenge: c.Challenge, Salt: c.Salt, MaxNumber: c.MaxNumber,
				Expires: c.Expires, Signature: c.Signature, Nonce: n,
			}
		}
	}
	t.Fatal("challenge has no solution")
	return nil
}
