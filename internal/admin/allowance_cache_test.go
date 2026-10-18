package admin

import "testing"

// A ranking lives in the usage cache under its own key, and is read back through
// a type assertion on a map of plain values. A key that drifted from the one the
// read builds, or a value stored under another type, would not fail anything:
// the ranking would simply never hit, and the page would rebuild it every time.
func TestAStoredRankingIsReadBackUnderTheKeyItWasStoredWith(t *testing.T) {
	h := &Handlers{}
	key := allowanceRankingKey("group-1", "ada")
	h.cacheRankingIfCurrent(key, rankedAllowances{matching: 3}, h.allowanceGenerationNow())

	ranked, ok := cachedRanking(h.getSummaryCache(key))
	if !ok || ranked.matching != 3 {
		t.Fatalf("stored ranking read back as %+v (found %v), want the one stored", ranked, ok)
	}
	if _, ok := cachedRanking(h.getSummaryCache(allowanceRankingKey("group-1", "bob"))); ok {
		t.Error("a ranking for another search was served from this one's entry")
	}

	h.forgetAllowanceRankings()
	if _, ok := cachedRanking(h.getSummaryCache(key)); ok {
		t.Error("forgetting the rankings left one behind")
	}
}
