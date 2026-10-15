package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// The reservation covers the pictures asked for and no more. A provider that
// answers with extra ones is billed, and its pictures stored, for the count
// that was asked for; the surplus never reaches the ledger or the library.
func TestAProviderThatSendsMoreThanAskedIsBilledForWhatWasAsked(t *testing.T) {
	lab := newImageLab(t, 3)
	lab.fixture.upstream.reply(`{"created":1,"data":[` +
		`{"b64_json":"` + generatedPNG + `"},` +
		`{"b64_json":"` + generatedPNG + `"},` +
		`{"b64_json":"` + generatedPNG + `"}]}`)

	recorder := lab.generate(t, `{"model_id":"`+lab.painter.ID+`","prompt":"a sunset"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Images []json.RawMessage `json:"images"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Images) != 1 {
		t.Errorf("returned %d pictures for one asked for", len(body.Images))
	}
	records := lab.ledger()
	if len(records) != 1 || records[0].Credits != 3 {
		t.Fatalf("ledger = %+v, want one record charging one picture (3 credits)", records)
	}
}

// Pictures the provider only linked to are read together, each on its own
// budget. A stalled URL spends its own time and nothing else: the pictures
// beside it must still arrive, or they are dropped while the call is billed.
func TestAStalledPictureURLDoesNotStarveThePicturesBesideIt(t *testing.T) {
	read := func(ctx context.Context, img adapter.GeneratedImage, _ int64) ([]byte, string, error) {
		if img.URL == "stalled" {
			<-ctx.Done()
			return nil, "", ctx.Err()
		}
		return []byte(img.URL), "image/png", nil
	}
	items := []adapter.GeneratedImage{{URL: "stalled"}, {URL: "first"}, {URL: "second"}}

	got := readPictures(context.Background(), items, 0, 50*time.Millisecond, read)
	if got[0].err == nil {
		t.Fatal("the stalled picture was not reported as failed")
	}
	if got[1].err != nil || string(got[1].data) != "first" {
		t.Errorf("first picture = %q, %v; want it delivered", got[1].data, got[1].err)
	}
	if got[2].err != nil || string(got[2].data) != "second" {
		t.Errorf("second picture = %q, %v; want it delivered", got[2].data, got[2].err)
	}
}

// A panic while reading one picture ends that picture, not the process. The
// read runs on its own goroutine, outside the handler's recover, so the
// goroutine has to catch it itself.
func TestAPanickingPictureReadIsReportedAsUnusable(t *testing.T) {
	read := func(_ context.Context, img adapter.GeneratedImage, _ int64) ([]byte, string, error) {
		if img.URL == "panics" {
			panic("decoder bug")
		}
		return []byte(img.URL), "image/png", nil
	}
	items := []adapter.GeneratedImage{{URL: "panics"}, {URL: "fine"}}

	got := readPictures(context.Background(), items, 0, time.Second, read)
	if !errors.Is(got[0].err, errImageMedia) {
		t.Fatalf("the panicking picture reported %v, want errImageMedia", got[0].err)
	}
	if got[1].err != nil || string(got[1].data) != "fine" {
		t.Errorf("the picture beside it = %q, %v; want it delivered", got[1].data, got[1].err)
	}
}
