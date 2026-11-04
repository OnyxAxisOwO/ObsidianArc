package consolessh

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Starts that race on one fresh data directory each generate a key. Each must
// come away with the key that is on disk: a start that signs with its own key,
// and was then renamed over, shows a fingerprint the next restart does not.
func TestConcurrentFirstStartsAgreeOnTheKeyOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh_host_ed25519_key")

	const starts = 16
	fingerprints := make([]string, starts)
	errs := make([]error, starts)
	begin := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < starts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-begin
			signer, err := loadOrCreateHostKey(path)
			if err != nil {
				errs[i] = err
				return
			}
			fingerprints[i] = fingerprint(signer)
		}(i)
	}
	close(begin)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	onDisk, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	want := fingerprint(onDisk)
	for i, got := range fingerprints {
		if got != want {
			t.Fatalf("start %d signs with %s, but the key on disk is %s", i, got, want)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries after the starts, want only the host key", len(entries))
	}
}
