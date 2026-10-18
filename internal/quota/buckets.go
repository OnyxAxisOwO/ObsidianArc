package quota

import (
	"time"
)

// bucketStart is the moment the window a turn falls in began.
//
// The rate windows stay on the wall clock: a minute is a minute, and giving
// each account its own would only make two accounts disagree about when one
// ends.
//
// The allowance windows are anchored to the account, and run from the moment
// it registered. They used to roll over at the same instant for everybody,
// which is two problems in one: somebody who signed up on the 28th got three
// days of a "month", and every allowance on the instance came back at
// midnight on the 1st, which is when everyone arrives at once.
//
// `anchor` is the account's created_at in epoch milliseconds. Zero — an
// account with no creation time, which no live row has — falls back to the
// epoch, so the arithmetic below is still well defined.
func bucketStart(window Window, now time.Time, anchor int64) int64 {
	switch window {
	case WindowRPM, WindowTPM:
		return now.Truncate(time.Minute).UnixMilli()
	case Window5H:
		return periodStart(now, anchor, 5*time.Hour)
	case WindowWeek:
		return periodStart(now, anchor, 7*24*time.Hour)
	case WindowMonth:
		from := time.UnixMilli(anchor).UTC()
		return addMonths(from, monthsSince(now, from)).UnixMilli()
	default:
		return now.UnixMilli()
	}
}

// periodStart walks whole periods from the anchor to the one `now` is in.
func periodStart(now time.Time, anchor int64, period time.Duration) int64 {
	elapsed := now.UnixMilli() - anchor
	if elapsed <= 0 {
		// A clock behind the account's own creation. There is nothing to
		// have spent yet, so it counts as the first period.
		return anchor
	}
	size := period.Milliseconds()
	return anchor + (elapsed/size)*size
}

// monthsSince is how many whole calendar months have passed since the anchor,
// so a monthly allowance renews on the day of the month somebody signed up on
// rather than every thirty days, drifting backwards through the year.
func monthsSince(now time.Time, from time.Time) int {
	utc := now.UTC()
	if !utc.After(from) {
		return 0
	}
	months := (utc.Year()-from.Year())*12 + int(utc.Month()) - int(from.Month())
	if addMonths(from, months).After(utc) {
		// The day of the month has not come round yet this month.
		months--
	}
	if months < 0 {
		return 0
	}
	return months
}

// addMonths keeps the day of the month, clamped to the length of the target
// one: an account created on the 31st renews on the 28th in February rather
// than sliding into March, which is what AddDate would do with it.
func addMonths(from time.Time, months int) time.Time {
	year, month, day := from.Date()
	hour, minute, second := from.Clock()
	target := time.Date(year, month+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(target.Year(), target.Month(), day, hour, minute, second, 0, time.UTC)
}

func bucketEnd(window Window, now time.Time, anchor int64) time.Time {
	start := time.UnixMilli(bucketStart(window, now, anchor)).UTC()
	switch window {
	case WindowRPM, WindowTPM:
		return start.Add(time.Minute)
	case Window5H:
		return start.Add(5 * time.Hour)
	case WindowWeek:
		return start.AddDate(0, 0, 7)
	case WindowMonth:
		// Not start.AddDate(0, 1, 0): the anchor's day of the month is what
		// the next one lands on, and adding a month to a clamped 28th would
		// walk the renewal backwards a day at a time.
		from := time.UnixMilli(anchor).UTC()
		return addMonths(from, monthsSince(now, from)+1)
	default:
		return start
	}
}

func round(value float64) float64 {
	return float64(int64(value*1000+0.5)) / 1000
}
