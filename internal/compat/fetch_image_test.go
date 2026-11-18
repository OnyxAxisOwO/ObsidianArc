package compat

import (
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

func TestURLOnlyImageDeliveryRequiresPublicDestination(t *testing.T) {
	f := newFixture(t)
	painter := imageModel(t, f, "painter", 0)
	override := `{"image_response_format":"url"}`
	if _, err := f.models.Update(t.Context(), painter.ID, model.Update{RequestOverride: &override}); err != nil {
		t.Fatal(err)
	}
	f.upstream.reply(`{"created":1,"data":[{"url":"http://127.0.0.1/image.png"}]}`)
	reply := f.do(t, http.MethodPost, "/v1/images/generations", f.token, `{"model":"painter","prompt":"lake"}`)
	if f.upstream.received()["response_format"] != "url" || reply.Code != http.StatusBadRequest {
		t.Fatalf("delivery: %d %s", reply.Code, reply.Body.String())
	}
}
