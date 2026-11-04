package settings

import (
	"testing"
)

// An AVIF whose ftyp box is 256 bytes long starts with the bytes an icon starts
// with. It must be an AVIF, which the logo sniffer used to refuse as an icon.
func TestAnAVIFWithA256ByteFtypBoxIsNotTakenForAnIcon(t *testing.T) {
	data := append([]byte{0x00, 0x00, 0x01, 0x00, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f'}, make([]byte, 244)...)

	if got, err := DetectLogoMedia(data); err != nil || got != "image/avif" {
		t.Errorf("DetectLogoMedia = %q, %v; want image/avif", got, err)
	}
	if got, err := DetectImageMedia(data); err != nil || got != "image/avif" {
		t.Errorf("DetectImageMedia = %q, %v; want image/avif", got, err)
	}
}

// The logo and the login background used to sniff the shared formats twice, in
// two copies that could drift. Both must give the same answer for each.
func TestTheSharedRasterFormatsAreSniffedTheSameWayForBothUses(t *testing.T) {
	samples := map[string][]byte{
		"png":  []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"),
		"jpeg": {0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0, 1},
		"webp": []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
		"avif": []byte("\x00\x00\x00\x18ftypavif\x00\x00\x00\x00mif1avif"),
	}
	want := map[string]string{
		"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp", "avif": "image/avif",
	}
	for name, data := range samples {
		logo, logoErr := DetectLogoMedia(data)
		background, backgroundErr := DetectImageMedia(data)
		if logo != want[name] || logoErr != nil {
			t.Errorf("%s: DetectLogoMedia = %q, %v; want %q", name, logo, logoErr, want[name])
		}
		if background != want[name] || backgroundErr != nil {
			t.Errorf("%s: DetectImageMedia = %q, %v; want %q", name, background, backgroundErr, want[name])
		}
	}
}

// The type the site logo was stored as is what the manifest declares for the
// logo's URL, which has no extension. It must be the sniffed type, whatever the
// upload claimed, and it must survive a restart.
func TestTheStoredLogoTypeIsTheOneTheBytesShowAndSurvivesARestart(t *testing.T) {
	ctx := t.Context()
	db, svc := openMigratedSettings(t)

	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0}
	if _, err := svc.SetSiteLogo(ctx, jpeg); err != nil {
		t.Fatalf("set logo: %v", err)
	}
	if got := svc.SiteLogoMime(); got != "image/jpeg" {
		t.Fatalf("cached type = %q, want image/jpeg", got)
	}

	reloaded := New(db)
	if err := reloaded.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := reloaded.SiteLogoMime(); got != "image/jpeg" {
		t.Fatalf("after a restart the type is %q, want image/jpeg", got)
	}

	if err := svc.DeleteSiteLogo(ctx); err != nil {
		t.Fatalf("delete logo: %v", err)
	}
	if got := svc.SiteLogoMime(); got != "" {
		t.Fatalf("after delete the type is %q, want none", got)
	}
}
