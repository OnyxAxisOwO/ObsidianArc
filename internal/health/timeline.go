package health

import (
	"context"
	"fmt"
	"time"
)

// TimePoint is evidence aggregated into one time bucket for graphing.
type TimePoint struct {
	At     int64    `json:"at"`
	Uptime *float64 `json:"uptime"`
	Total  int      `json:"total"`
}

// Timeline groups recent evidence for each model into uniform time buckets so
// that callers can render a trend without pulling thousands of raw samples.
//
// If resetAt is non-zero, evidence before resetAt is excluded from the buckets
// while preserving the uniform time span across the requested window.
func (s *Store) Timeline(ctx context.Context, since int64, resetAt int64, buckets int) (map[string][]TimePoint, error) {
	if buckets <= 0 {
		buckets = 24
	}
	now := time.Now().UnixMilli()
	if resetAt > now {
		resetAt = now
	}
	if since >= now {
		since = now - 24*time.Hour.Milliseconds()
	}
	bucketSpan := (now - since) / int64(buckets)
	if bucketSpan <= 0 {
		bucketSpan = 1
	}

	evidenceSince := since
	if resetAt > evidenceSince {
		evidenceSince = resetAt
	}

	// The bucket is computed and counted by the database. Reading every row of
	// the window to bucket it in Go made each uptime request cost one row per
	// request the whole fleet served in the last 24 hours. The bucket is the
	// same integer division as before, and the index is clamped in Go below so
	// the expression needs no CASE or MIN, which the two engines spell apart.
	rows, err := s.db.Query(ctx, `
		SELECT model_id, idx, COUNT(*), SUM(ok) FROM (
			SELECT model_id, (at - ?) / ? AS idx, ok FROM (
				SELECT model_id, started_at AS at,
				       CASE WHEN status = ? THEN 1 ELSE 0 END AS ok
				FROM usage_records
				WHERE started_at >= ? AND status IN (?, ?)
				UNION ALL
				SELECT model_id, at, CASE WHEN ok THEN 1 ELSE 0 END AS ok
				FROM model_probes
				WHERE at >= ?
			) AS evidence
		) AS buckets
		GROUP BY model_id, idx`,
		since, bucketSpan,
		statusOK, evidenceSince, statusOK, statusError,
		evidenceSince)
	if err != nil {
		return nil, fmt.Errorf("health: timeline: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type count struct {
		total int
		ok    int
	}
	byModel := map[string][]count{}

	for rows.Next() {
		var modelID string
		var idx, total, ok int64
		if err := rows.Scan(&modelID, &idx, &total, &ok); err != nil {
			return nil, fmt.Errorf("health: timeline: %w", err)
		}
		list, exists := byModel[modelID]
		if !exists {
			list = make([]count, buckets)
			byModel[modelID] = list
		}
		// Evidence is never before since, so the low clamp is a guard; the high
		// one folds the rounding remainder and any row stamped after now into
		// the last bucket, as it always was.
		if idx < 0 {
			idx = 0
		}
		if idx >= int64(buckets) {
			idx = int64(buckets - 1)
		}
		list[idx].total += int(total)
		list[idx].ok += int(ok)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("health: timeline: %w", err)
	}

	result := make(map[string][]TimePoint, len(byModel))
	for modelID, list := range byModel {
		pts := make([]TimePoint, buckets)
		for i := 0; i < buckets; i++ {
			at := since + int64(i)*bucketSpan + bucketSpan/2
			pt := TimePoint{At: at, Total: list[i].total}
			if list[i].total > 0 {
				u := float64(list[i].ok) / float64(list[i].total)
				pt.Uptime = &u
			}
			pts[i] = pt
		}
		result[modelID] = pts
	}
	return result, nil
}
