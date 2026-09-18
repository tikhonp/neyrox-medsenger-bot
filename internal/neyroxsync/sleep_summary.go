package neyroxsync

import (
	"database/sql"
	"errors"
	"log"
	"math"
	"sort"
	"time"

	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db/models"
	neyroxclient "github.com/tikhonp/medsenger-neyrox-bot/internal/util/neyrox_client"
)

// sleepSummaryMetric is both the Neyrox endpoint and the watermark key for the sleep
// summary fallback.
//
// /api/v1/sleep/ holds no intervals. A row is the running total of one stage — deep,
// light or REM, in hours at 0.1 h resolution — for the sleep in progress, re-sent every
// 5–20 minutes while the band thinks the wearer is asleep. It is the only sleep data a band
// whose firmware sends no hypnogram produces, so for a night no hypnogram covers (see
// hypnogramCovers) the totals are turned into stage intervals at reporting resolution:
// the hours a stage gained between two reports are laid inside that window. Which order
// the stages came in inside a window is not known, so it is fixed (sleepSummaryOrder).
// The per-stage totals of a night are faithful; their placement is only as fine as the
// reporting interval. A night with a real hypnogram is left to sleep.go.
const sleepSummaryMetric = "sleep"

const (
	// sleepSessionGap separates sessions: two reports further apart belong to different
	// sleeps. It also bounds how far a late-arriving tail of an already pushed night chains.
	sleepSessionGap = 3 * time.Hour
	// sleepSummaryHold is how old a session's newest report must be before the session is
	// treated as complete. Rows reach Neyrox when the phone syncs (minutes as a rule, ~6 h
	// at the 90th percentile observed), and the night's hypnogram, if the band makes one,
	// travels the same way — by then it has had its chance to arrive first.
	sleepSummaryHold = 6 * time.Hour
	// hypnogramLookback is how far before a session's start hypnograms are fetched when
	// checking whether one already describes the night.
	hypnogramLookback = 24 * time.Hour
)

// sleepSummaryOrder is the order stage hours are laid inside one reporting window, and the
// only categories the summary can fill. The endpoint has no awake counter, so window time
// no stage accounts for stays unlabeled rather than being called awake.
var sleepSummaryOrder = []string{categoryAsleepCore, categoryAsleepDeep, categoryAsleepREM}

// sleepRow is one /api/v1/sleep/ row resolved to its Medsenger category.
type sleepRow struct {
	at       time.Time
	category string
	hours    float64
}

// sleepSnapshot is one report: the running total of every stage (hours) at that moment.
// A report that omitted a stage carries the previous value forward.
type sleepSnapshot struct {
	at    time.Time
	hours map[string]float64
}

func (s sleepSnapshot) sum() float64 {
	var total float64
	for _, cat := range sleepSummaryOrder {
		total += s.hours[cat]
	}
	return total
}

// sleepSession is one sleep: its snapshots in time order.
type sleepSession []sleepSnapshot

func (ss sleepSession) first() sleepSnapshot { return ss[0] }
func (ss sleepSession) last() sleepSnapshot  { return ss[len(ss)-1] }

// closed reports whether the session can be treated as complete: nothing has been
// reported for it for sleepSummaryHold.
func (ss sleepSession) closed(now time.Time) bool {
	return now.Sub(ss.last().at) >= sleepSummaryHold
}

// accepted trims the session to the reports that carry information: the first one, then
// every one whose total is a strict new high. That drops plateaus, single-report dips
// (`2.9/1.2/-` between 3.6 and 4.3 h) and the post-wake tail, where the band keeps
// re-sending fluctuating summaries (5.3/2.8/0.9 h at the morning flush, then 3.3 → 2.2 →
// 3.5 → 5.4 → 4.2 for three more hours). The last accepted report is the night's end and
// holds its totals.
func (ss sleepSession) accepted() []sleepSnapshot {
	const eps = 1e-6
	out := make([]sleepSnapshot, 0, len(ss))
	var best float64
	for i, snap := range ss {
		total := snap.sum()
		if i == 0 || total > best+eps {
			out = append(out, snap)
			if total > best {
				best = total
			}
		}
	}
	return out
}

// sleepSessions folds rows into snapshots and splits them into sessions on gaps longer
// than sleepSessionGap. Carry-forward of a stage a report omitted never crosses a session
// boundary: a new sleep starts from zero.
func sleepSessions(rows []sleepRow) []sleepSession {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })

	var sessions []sleepSession
	var cur sleepSession
	for _, r := range rows {
		if len(cur) > 0 {
			last := &cur[len(cur)-1]
			switch {
			case r.at.Equal(last.at):
				last.hours[r.category] = r.hours
				continue
			case r.at.Sub(last.at) > sleepSessionGap:
				sessions = append(sessions, cur)
				cur = nil
			}
		}
		snap := sleepSnapshot{at: r.at, hours: make(map[string]float64, len(sleepSummaryOrder))}
		if len(cur) > 0 {
			for cat, h := range cur[len(cur)-1].hours {
				snap.hours[cat] = h
			}
		}
		snap.hours[r.category] = r.hours
		cur = append(cur, snap)
	}
	if len(cur) > 0 {
		sessions = append(sessions, cur)
	}
	return sessions
}

// hoursDuration converts a reported hour count to a duration, rounded to the second so
// that 0.1 h steps come out as clean 6 minute spans.
func hoursDuration(hours float64) time.Duration {
	return time.Duration(math.Round(hours*3600)) * time.Second
}

// sessionRecords turns a session's accepted reports into interval records and returns
// them with the span they cover.
//
// The first report already holds what accumulated before it was sent, so the sleep is taken
// to have begun that many hours earlier and that block is laid before it. Every later
// report adds, inside the window since the previous one, the hours each stage gained. When
// the 0.1 h steps add up to more than the window, the excess is carried into the next one
// (and dropped at the end); when they add up to less, the rest of the window stays
// unlabeled. One in_bed record spans the whole session, as nightRecords does for a
// hypnogram.
func sessionRecords(snaps []sleepSnapshot) (records []maigo.Record, start, end time.Time) {
	if len(snaps) == 0 {
		return nil, start, end
	}
	first := snaps[0]
	start = first.at.Add(-hoursDuration(first.sum()))
	end = snaps[len(snaps)-1].at

	carry := make(map[string]time.Duration, len(sleepSummaryOrder))
	initial := make(map[string]time.Duration, len(sleepSummaryOrder))
	for _, cat := range sleepSummaryOrder {
		initial[cat] = hoursDuration(first.hours[cat])
	}
	records = layWindow(start, first.at, initial, carry, records)

	prev := first
	for _, cur := range snaps[1:] {
		gained := make(map[string]time.Duration, len(sleepSummaryOrder))
		for _, cat := range sleepSummaryOrder {
			if d := cur.hours[cat] - prev.hours[cat]; d > 0 {
				gained[cat] = hoursDuration(d)
			}
		}
		records = layWindow(prev.at, cur.at, gained, carry, records)
		prev = cur
	}

	if len(records) > 0 && end.After(start) {
		records = append(records, newIntervalRecord(categoryInBed, start, end))
	}
	return records, start, end
}

// layWindow appends one interval per stage that gained time in [start, end], laid end to
// end from start in sleepSummaryOrder. What does not fit is left in carry for the next
// window.
func layWindow(start, end time.Time, gained, carry map[string]time.Duration, records []maigo.Record) []maigo.Record {
	cursor := start
	for _, cat := range sleepSummaryOrder {
		d := gained[cat] + carry[cat]
		carry[cat] = 0
		if d <= 0 {
			continue
		}
		if room := end.Sub(cursor); d > room {
			carry[cat] = d - room
			d = room
		}
		if d <= 0 {
			continue
		}
		records = append(records, newIntervalRecord(cat, cursor, cursor.Add(d)))
		cursor = cursor.Add(d)
	}
	return records
}

// hypnogramsOverlap reports whether any of the hypnograms describes sleep inside
// [start, end]. A hypnogram that does not decode into intervals does not count: the
// hypnogram path pushed nothing for it, so the summary is all that night has.
func hypnogramsOverlap(hypnograms []neyroxclient.Hypnogram, start, end time.Time) bool {
	for _, h := range hypnograms {
		intervals, err := neyroxclient.ParseIntervals(h)
		if err != nil || len(intervals) == 0 {
			continue
		}
		from, to := intervals[0].Start, intervals[0].End
		for _, iv := range intervals[1:] {
			if iv.End.After(to) {
				to = iv.End
			}
		}
		if from.Before(end) && to.After(start) {
			return true
		}
	}
	return false
}

// hypnogramCovers reports whether Neyrox already has a hypnogram for the sleep in
// [start, end], in which case sleep.go has pushed (or will push) the real intervals.
func (s *Syncer) hypnogramCovers(access string, start, end time.Time) (bool, error) {
	since := start.Add(-hypnogramLookback)
	hypnograms, err := s.nc.FetchHypnograms(access, &since)
	if err != nil {
		return false, err
	}
	return hypnogramsOverlap(hypnograms, start, end), nil
}

// sleepStageRows resolves measurements to the stage categories the summary can fill,
// through the indicator names (the same substring mapping hypnogram stages use). Rows of
// any other indicator — the endpoint also carries a "sleep quality" score — are dropped.
func (s *Syncer) sleepStageRows(measurements []neyroxclient.Measurement) []sleepRow {
	rows := make([]sleepRow, 0, len(measurements))
	for _, m := range measurements {
		if m.Value == nil {
			continue
		}
		name, ok := s.indicators[m.TypeIndicator]
		if !ok {
			continue
		}
		category, known := sleepCategory(name)
		if !known || !isSummaryStage(category) {
			continue
		}
		rows = append(rows, sleepRow{at: m.DateDevice, category: category, hours: *m.Value})
	}
	return rows
}

func isSummaryStage(category string) bool {
	for _, cat := range sleepSummaryOrder {
		if cat == category {
			return true
		}
	}
	return false
}

// synthesizeSleep walks the sessions oldest first and appends, for every complete one that
// covered says has no hypnogram, the intervals synthesized from its reports. It returns
// the new watermark: the newest report of the last session dealt with, pushed or skipped.
//
// date_device_after is inclusive, so a fetch starting at that watermark begins with that
// very report. A session that begins exactly at the watermark is therefore the tail of a
// night already pushed — that report, plus anything uploaded late that chains onto it —
// and is dropped, only moving the watermark on. A watermark seeded from the account's
// LastSync, or set by hand for a backfill, never coincides with a report, so nothing is
// dropped after it.
//
// The walk stops at the first session still open (a later one cannot be complete while an
// earlier one is not) and at the first covered error, leaving that session and everything
// after it for the next cycle rather than risking a night pushed twice.
func synthesizeSleep(
	contractID int, sessions []sleepSession, watermark sql.NullTime, now time.Time,
	covered func(start, end time.Time) (bool, error), records *[]maigo.Record,
) (sql.NullTime, error) {
	var newest sql.NullTime
	for i, session := range sessions {
		if i == 0 && watermark.Valid && session.first().at.Equal(watermark.Time) {
			newest = laterOf(newest, session.last().at)
			continue
		}
		if !session.closed(now) {
			break
		}

		recs, start, end := sessionRecords(session.accepted())
		if len(recs) > 0 {
			isCovered, err := covered(start, end)
			if err != nil {
				return newest, err
			}
			if isCovered {
				log.Printf("contract %d: sleep %s–%s has a hypnogram, summary skipped",
					contractID, start.Format(time.RFC3339), end.Format(time.RFC3339))
			} else {
				log.Printf("contract %d: sleep %s–%s synthesized from summaries (%d records)",
					contractID, start.Format(time.RFC3339), end.Format(time.RFC3339), len(recs))
				*records = append(*records, recs...)
			}
		}
		newest = laterOf(newest, session.last().at)
	}
	return newest, nil
}

// appendSleepSummaries appends one account's sleep intervals synthesized from its sleep
// summaries (see synthesizeSleep). Returns the new watermark, plus the endpoint to blame if
// the error is non-nil.
func (s *Syncer) appendSleepSummaries(
	acc *models.NeyroxAccount, access string, watermark sql.NullTime, records *[]maigo.Record,
) (sql.NullTime, string, error) {
	var newest sql.NullTime

	if err := s.loadIndicators(access); err != nil {
		if errors.Is(err, neyroxclient.ErrUnauthorized) {
			return newest, "typeindicators", err
		}
		log.Printf("contract %d: resolve sleep indicators: %v", acc.ContractID, err)
		return newest, sleepSummaryMetric, nil
	}

	// As for hypnograms, only an expired token is fatal: the scalar metrics collected this
	// cycle must not be thrown away over one source.
	measurements, err := s.nc.FetchMeasurements(access, sleepSummaryMetric, sinceTime(watermark))
	if errors.Is(err, neyroxclient.ErrUnauthorized) {
		return newest, sleepSummaryMetric, err
	}
	if err != nil {
		log.Printf("contract %d: fetch sleep summaries: %v", acc.ContractID, err)
		return newest, sleepSummaryMetric, nil
	}

	covered := func(start, end time.Time) (bool, error) {
		return s.hypnogramCovers(access, start, end)
	}
	sessions := sleepSessions(s.sleepStageRows(measurements))
	newest, err = synthesizeSleep(acc.ContractID, sessions, watermark, time.Now(), covered, records)
	if errors.Is(err, neyroxclient.ErrUnauthorized) {
		return newest, hypnogramMetric, err
	}
	if err != nil {
		// Sessions handled before the failure stand; the watermark stops in front of it.
		log.Printf("contract %d: check hypnogram for sleep summaries: %v", acc.ContractID, err)
	}
	return newest, sleepSummaryMetric, nil
}
