package cardgrant

import (
	"net/http"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/server/servertest"
)

func TestMassGrantFlow(t *testing.T) {
	in := servertest.New(t)
	admin := in.Register("admin", "admin-pass-1234")
	user1 := in.Register("user1", "user1-pass-1234")
	user2 := in.Register("user2", "user2-pass-1234")

	// 1. Regular user is refused.
	resp := in.Do(http.MethodPost, "/api/admin/cardgrant/grant", map[string]any{
		"name":   "Qwen 系列模型下架补偿",
		"window": "5h",
		"days":   7,
	}, user1)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("regular user grant: got %d, want 403", resp.Code)
	}

	// 2. Admin performs mass grant.
	resp = in.Do(http.MethodPost, "/api/admin/cardgrant/grant", map[string]any{
		"name":   "Qwen 系列模型下架补偿",
		"window": "5h",
		"days":   7,
	}, admin)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin grant: got %d %s", resp.Code, resp.Body.String())
	}

	var grantRes struct {
		Granted   int    `json:"granted"`
		Name      string `json:"name"`
		Window    string `json:"window"`
		ExpiresAt int64  `json:"expires_at"`
	}
	grantRes = servertest.Decode[struct {
		Granted   int    `json:"granted"`
		Name      string `json:"name"`
		Window    string `json:"window"`
		ExpiresAt int64  `json:"expires_at"`
	}](t, resp)

	// Admin + user1 + user2 = 3 users.
	if grantRes.Granted != 3 {
		t.Errorf("granted count = %d, want 3", grantRes.Granted)
	}
	if grantRes.Name != "Qwen 系列模型下架补偿" {
		t.Errorf("name = %q, want %q", grantRes.Name, "Qwen 系列模型下架补偿")
	}
	if grantRes.Window != "5h" {
		t.Errorf("window = %q, want %q", grantRes.Window, "5h")
	}

	// 3. User1 and User2 see their cards.
	for _, u := range []*servertest.Session{user1, user2} {
		cardsResp := in.Do(http.MethodGet, "/api/usage/cards", nil, u)
		if cardsResp.Code != http.StatusOK {
			t.Fatalf("get cards: %d %s", cardsResp.Code, cardsResp.Body.String())
		}
		cardsData := servertest.Decode[struct {
			Cards []struct {
				ID      string   `json:"id"`
				Name    string   `json:"name"`
				Windows []string `json:"windows"`
			} `json:"cards"`
		}](t, cardsResp)

		if len(cardsData.Cards) != 1 {
			t.Fatalf("user cards count = %d, want 1", len(cardsData.Cards))
		}
		if cardsData.Cards[0].Name != "Qwen 系列模型下架补偿" {
			t.Errorf("card name = %q", cardsData.Cards[0].Name)
		}
		if len(cardsData.Cards[0].Windows) != 1 || cardsData.Cards[0].Windows[0] != "5h" {
			t.Errorf("card windows = %v, want ['5h']", cardsData.Cards[0].Windows)
		}
	}

	// 4. Admin lists grant history.
	historyResp := in.Do(http.MethodGet, "/api/admin/cardgrant/grants", nil, admin)
	if historyResp.Code != http.StatusOK {
		t.Fatalf("get grants history: %d %s", historyResp.Code, historyResp.Body.String())
	}
	historyData := servertest.Decode[struct {
		Total  int `json:"total"`
		Grants []struct {
			Name           string `json:"name"`
			Windows        string `json:"windows"`
			RecipientCount int    `json:"recipient_count"`
		} `json:"grants"`
	}](t, historyResp)

	if historyData.Total != 1 {
		t.Errorf("history total = %d, want 1", historyData.Total)
	}
	if len(historyData.Grants) != 1 {
		t.Fatalf("history records count = %d, want 1", len(historyData.Grants))
	}
	if historyData.Grants[0].RecipientCount != 3 {
		t.Errorf("recipient count = %d, want 3", historyData.Grants[0].RecipientCount)
	}
}

func TestMassGrantValidation(t *testing.T) {
	in := servertest.New(t)
	admin := in.Register("admin", "admin-pass-1234")

	// Invalid window
	resp := in.Do(http.MethodPost, "/api/admin/cardgrant/grant", map[string]any{
		"name":   "Test",
		"window": "2h",
		"days":   7,
	}, admin)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("invalid window: got %d, want 400", resp.Code)
	}

	// Past expiry
	past := time.Now().Add(-time.Hour).UnixMilli()
	resp = in.Do(http.MethodPost, "/api/admin/cardgrant/grant", map[string]any{
		"name":       "Test",
		"window":     "5h",
		"expires_at": past,
	}, admin)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("past expiry: got %d, want 400", resp.Code)
	}
}
