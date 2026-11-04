package quota

import (
	"errors"
	"sync"
)

// How many generations one account may have running at once.
//
// Reserving the ceiling already bounds what concurrent turns can spend, but
// only for an account that has a limit at all; an unlimited one, or a window
// that is measured and not enforced, would still let a script hold open as
// many upstream connections as it likes. This is the backstop on connections
// rather than on cost, and it is small because a person cannot read four
// answers at once.
const DefaultMaxConcurrent = 4

var ErrTooManyInFlight = errors.New("quota: too many generations in flight for this account")

// Begin claims a slot and returns the release. The release is idempotent, so
// a caller may defer it and still call it early. A maxConcurrent <= 0 means
// unlimited concurrency for the account.
//
// In memory, like the other counters that exist only to shape one process's
// behaviour: two instances behind a load balancer each enforce their own, and
// the cost ceiling ReserveFor takes is what holds in that case.
func (s *Service) Begin(userID string, maxConcurrent int) (func(), error) {
	if maxConcurrent <= 0 {
		return func() {}, nil
	}
	s.mu.Lock()
	if s.inFlight == nil {
		s.inFlight = map[string]int{}
	}
	if s.inFlight[userID] >= maxConcurrent {
		s.mu.Unlock()
		return nil, ErrTooManyInFlight
	}
	s.inFlight[userID]++
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.inFlight[userID] <= 1 {
				delete(s.inFlight, userID)
			} else {
				s.inFlight[userID]--
			}
			s.mu.Unlock()
		})
	}, nil
}
