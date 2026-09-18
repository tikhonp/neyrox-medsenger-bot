package neyroxsync

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tikhonp/maigo"
	neyroxclient "github.com/tikhonp/medsenger-neyrox-bot/internal/util/neyrox_client"
)

// at returns a time on the test night, counted from the evening of 20 July 2026 (UTC);
// hours of 24 and more roll into the next morning.
func at(hour, minute int) time.Time {
	return time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).
		Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func snapshot(t time.Time, core, deep, rem float64) sleepSnapshot {
	return sleepSnapshot{at: t, hours: map[string]float64{
		categoryAsleepCore: core, categoryAsleepDeep: deep, categoryAsleepREM: rem,
	}}
}

// describe renders a record as "category start..end" for compact comparisons.
func describe(t *testing.T, r maigo.Record) string {
	t.Helper()
	value, ok := r.Value.(string)
	if !ok {
		t.Fatalf("record value %v is %T, want string", r.Value, r.Value)
	}
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		t.Fatalf("record value %q is not \"start,end\"", value)
	}
	var ends [2]string
	for i, p := range parts {
		unix, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			t.Fatalf("record value %q: %v", value, err)
		}
		ends[i] = time.Unix(unix, 0).UTC().Format("02T15:04")
	}
	return fmt.Sprintf("%s %s..%s", r.CategoryName, ends[0], ends[1])
}

func describeAll(t *testing.T, records []maigo.Record) []string {
	t.Helper()
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, describe(t, r))
	}
	return out
}

func TestSleepSessionsFoldsRowsIntoSnapshots(t *testing.T) {
	rows := []sleepRow{
		// Deliberately unsorted; two rows share the first timestamp.
		{at(24, 9), categoryAsleepDeep, 0.5},
		{at(23, 47), categoryAsleepCore, 0.9},
		{at(23, 47), categoryAsleepDeep, 0.2},
		{at(24, 33), categoryAsleepREM, 0.1},
		// 3 h 27 min later: a different sleep, which must not inherit the totals above.
		{at(28, 0), categoryAsleepCore, 1.0},
	}
	sessions := sleepSessions(rows)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	want := []sleepSnapshot{
		snapshot(at(23, 47), 0.9, 0.2, 0),
		snapshot(at(24, 9), 0.9, 0.5, 0), // core carried forward
		snapshot(at(24, 33), 0.9, 0.5, 0.1),
	}
	if got := sessions[0]; len(got) != len(want) {
		t.Fatalf("first session has %d snapshots, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := sessions[0][i]
		if !g.at.Equal(w.at) {
			t.Errorf("snapshot %d at %s, want %s", i, g.at, w.at)
		}
		for _, cat := range sleepSummaryOrder {
			if g.hours[cat] != w.hours[cat] {
				t.Errorf("snapshot %d %s = %v, want %v", i, cat, g.hours[cat], w.hours[cat])
			}
		}
	}
	second := sessions[1]
	if len(second) != 1 || second[0].hours[categoryAsleepCore] != 1.0 || second[0].hours[categoryAsleepDeep] != 0 {
		t.Errorf("second session = %+v, want one snapshot with core 1.0 and nothing carried over", second)
	}
}

func TestSleepSessionClosed(t *testing.T) {
	session := sleepSession{snapshot(at(23, 0), 0.5, 0, 0), snapshot(at(31, 1), 3, 4, 1)}
	if !session.closed(at(31, 1).Add(sleepSummaryHold)) {
		t.Error("session with its newest report sleepSummaryHold ago is not closed")
	}
	if session.closed(at(31, 1).Add(sleepSummaryHold - time.Minute)) {
		t.Error("session with a report newer than sleepSummaryHold is closed")
	}
}

// The shape of the night of 20–21 July 2026 as the band reported it: a single-report dip
// at 05:11, a plateau at the 07:01 flush, then three hours of fluctuating re-sends.
func TestSleepSessionAcceptedTrimsDipsPlateausAndTail(t *testing.T) {
	session := sleepSession{
		snapshot(at(28, 50), 2.5, 3.6, 0.8),
		snapshot(at(29, 11), 1.2, 2.9, 0.8), // dip
		snapshot(at(29, 28), 2.5, 4.3, 0.8),
		snapshot(at(31, 0), 2.8, 5.3, 0.9),
		snapshot(at(31, 1), 2.8, 5.3, 0.9),  // plateau
		snapshot(at(31, 12), 1.1, 3.3, 0.9), // post-wake tail
		snapshot(at(33, 41), 1.6, 5.4, 0.9),
		snapshot(at(33, 57), 1.5, 4.2, 0.9),
	}
	got := session.accepted()
	want := []time.Time{at(28, 50), at(29, 28), at(31, 0)}
	if len(got) != len(want) {
		t.Fatalf("accepted %d snapshots, want %d", len(got), len(want))
	}
	for i, w := range want {
		if !got[i].at.Equal(w) {
			t.Errorf("accepted[%d] at %s, want %s", i, got[i].at, w)
		}
	}
	last := got[len(got)-1]
	if last.hours[categoryAsleepDeep] != 5.3 || last.hours[categoryAsleepCore] != 2.8 || last.hours[categoryAsleepREM] != 0.9 {
		t.Errorf("night totals = %v, want deep 5.3 / core 2.8 / REM 0.9", last.hours)
	}
}

func TestSessionRecordsLaysStagesInsideReportingWindows(t *testing.T) {
	snaps := []sleepSnapshot{
		snapshot(at(23, 47), 0.9, 0.2, 0), // 1.1 h already accumulated: sleep began 22:41
		snapshot(at(24, 9), 0.9, 0.5, 0),  // +18 min deep in a 22 min window
		snapshot(at(24, 27), 0.9, 0.8, 0), // +18 min deep in an 18 min window
		snapshot(at(24, 33), 1.2, 0.8, 0), // +18 min core in a 6 min window: 12 min carried
		snapshot(at(24, 50), 1.2, 0.8, 0.1),
	}
	records, start, end := sessionRecords(snaps)
	if !start.Equal(at(22, 41)) || !end.Equal(at(24, 50)) {
		t.Errorf("span = %s..%s, want 22:41..00:50", start, end)
	}
	want := []string{
		"asleep_core 20T22:41..20T23:35",
		"asleep_deep 20T23:35..20T23:47",
		"asleep_deep 20T23:47..21T00:05", // 4 min of the window left unlabeled
		"asleep_deep 21T00:09..21T00:27",
		"asleep_core 21T00:27..21T00:33",
		"asleep_core 21T00:33..21T00:45", // the carried 12 min
		"asleep_REM 21T00:45..21T00:50",  // 6 min wanted, 5 fit; the rest is dropped at the end
		"in_bed 20T22:41..21T00:50",
	}
	got := describeAll(t, records)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, r := range records {
		if r.Time == nil || !r.Time.Equal(mustStart(t, r)) {
			t.Errorf("record %s is timestamped %v, want its start", describe(t, r), r.Time)
		}
	}
}

func mustStart(t *testing.T, r maigo.Record) time.Time {
	t.Helper()
	unix, err := strconv.ParseInt(strings.Split(r.Value.(string), ",")[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return time.Unix(unix, 0).UTC()
}

func TestSessionRecordsDegenerateSessions(t *testing.T) {
	if records, _, _ := sessionRecords(nil); len(records) != 0 {
		t.Errorf("no snapshots gave %d records", len(records))
	}
	if records, _, _ := sessionRecords([]sleepSnapshot{snapshot(at(23, 0), 0, 0, 0)}); len(records) != 0 {
		t.Errorf("a single empty report gave %d records, want none", len(records))
	}
	records, _, _ := sessionRecords([]sleepSnapshot{snapshot(at(23, 0), 0.5, 0, 0)})
	want := []string{"asleep_core 20T22:30..20T23:00", "in_bed 20T22:30..20T23:00"}
	if got := describeAll(t, records); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("single report gave %v, want %v", got, want)
	}
}

func hypnogram(start time.Time, data string) neyroxclient.Hypnogram {
	return neyroxclient.Hypnogram{ID: "h", DateDevice: start, Data: json.RawMessage(data)}
}

func TestHypnogramsOverlap(t *testing.T) {
	// One hour of deep sleep from 23:00.
	night := hypnogram(at(23, 0), `[{"stage":"Глубокий сон","time_long_sec":3600}]`)
	tests := []struct {
		name       string
		hypnograms []neyroxclient.Hypnogram
		start, end time.Time
		want       bool
	}{
		{"overlapping night", []neyroxclient.Hypnogram{night}, at(22, 41), at(24, 50), true},
		{"touching at the end only", []neyroxclient.Hypnogram{night}, at(24, 0), at(28, 0), false},
		{"other night", []neyroxclient.Hypnogram{night}, at(47, 0), at(55, 0), false},
		{"unreadable hypnogram does not count", []neyroxclient.Hypnogram{hypnogram(at(23, 0), `{"foo":1}`)}, at(22, 0), at(26, 0), false},
		{"empty hypnogram does not count", []neyroxclient.Hypnogram{hypnogram(at(23, 0), `null`)}, at(22, 0), at(26, 0), false},
		{"no hypnograms", nil, at(22, 0), at(26, 0), false},
	}
	for _, tt := range tests {
		if got := hypnogramsOverlap(tt.hypnograms, tt.start, tt.end); got != tt.want {
			t.Errorf("%s: hypnogramsOverlap = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Two complete nights and a watermark.
func twoNights() []sleepSession {
	return []sleepSession{
		{snapshot(at(23, 0), 0.5, 0, 0), snapshot(at(24, 0), 1.0, 0.5, 0), snapshot(at(31, 1), 3, 4, 1)},
		{snapshot(at(47, 0), 0.5, 0, 0), snapshot(at(48, 0), 1.0, 0.5, 0), snapshot(at(55, 1), 3, 4, 1)},
	}
}

func never(time.Time, time.Time) (bool, error) { return false, nil }

func countCategory(records []maigo.Record, category string) int {
	var n int
	for _, r := range records {
		if r.CategoryName == category {
			n++
		}
	}
	return n
}

func TestSynthesizeSleepPushesCompleteNights(t *testing.T) {
	var records []maigo.Record
	newest, err := synthesizeSleep(1, twoNights(), sql.NullTime{}, at(72, 0), never, &records)
	if err != nil {
		t.Fatal(err)
	}
	if n := countCategory(records, categoryInBed); n != 2 {
		t.Errorf("got %d in_bed records, want one per night", n)
	}
	if !newest.Valid || !newest.Time.Equal(at(55, 1)) {
		t.Errorf("watermark = %v, want the second night's last report", newest)
	}
}

func TestSynthesizeSleepDropsTailOfPushedNight(t *testing.T) {
	// The watermark is the first night's last report: that night was pushed already, and
	// the fetch (inclusive) returned that report plus one uploaded late that chains on it.
	sessions := twoNights()
	sessions[0] = sleepSession{snapshot(at(31, 1), 3, 4, 1), snapshot(at(31, 20), 3.2, 4, 1)}
	watermark := sql.NullTime{Valid: true, Time: at(31, 1)}

	var records []maigo.Record
	newest, err := synthesizeSleep(1, sessions, watermark, at(72, 0), never, &records)
	if err != nil {
		t.Fatal(err)
	}
	if n := countCategory(records, categoryInBed); n != 1 {
		t.Errorf("got %d in_bed records, want 1 (the tail must not become a night)", n)
	}
	for _, r := range records {
		if mustStart(t, r).Before(at(40, 0)) {
			t.Errorf("record %s comes from the dropped tail", describe(t, r))
		}
	}
	if !newest.Valid || !newest.Time.Equal(at(55, 1)) {
		t.Errorf("watermark = %v, want the second night's last report", newest)
	}
}

func TestSynthesizeSleepKeepsSessionStartingAfterSeededWatermark(t *testing.T) {
	// A watermark seeded from another metric's time never equals a report: nothing dropped.
	var records []maigo.Record
	watermark := sql.NullTime{Valid: true, Time: at(22, 59)}
	if _, err := synthesizeSleep(1, twoNights(), watermark, at(72, 0), never, &records); err != nil {
		t.Fatal(err)
	}
	if n := countCategory(records, categoryInBed); n != 2 {
		t.Errorf("got %d in_bed records, want 2", n)
	}
}

func TestSynthesizeSleepWaitsForOpenSession(t *testing.T) {
	var records []maigo.Record
	// Two hours after the second night's last report: it may still be growing.
	newest, err := synthesizeSleep(1, twoNights(), sql.NullTime{}, at(57, 0), never, &records)
	if err != nil {
		t.Fatal(err)
	}
	if n := countCategory(records, categoryInBed); n != 1 {
		t.Errorf("got %d in_bed records, want 1 (second night still open)", n)
	}
	if !newest.Valid || !newest.Time.Equal(at(31, 1)) {
		t.Errorf("watermark = %v, want the first night's last report", newest)
	}
}

func TestSynthesizeSleepSkipsCoveredNightButAdvances(t *testing.T) {
	var records []maigo.Record
	var checked []time.Time
	covered := func(start, end time.Time) (bool, error) {
		checked = append(checked, start)
		return start.Before(at(40, 0)), nil // first night has a hypnogram
	}
	newest, err := synthesizeSleep(1, twoNights(), sql.NullTime{}, at(72, 0), covered, &records)
	if err != nil {
		t.Fatal(err)
	}
	if len(checked) != 2 {
		t.Errorf("hypnogram coverage checked %d times, want once per night", len(checked))
	}
	if n := countCategory(records, categoryInBed); n != 1 {
		t.Errorf("got %d in_bed records, want 1 (covered night skipped)", n)
	}
	for _, r := range records {
		if mustStart(t, r).Before(at(40, 0)) {
			t.Errorf("record %s belongs to the covered night", describe(t, r))
		}
	}
	if !newest.Valid || !newest.Time.Equal(at(55, 1)) {
		t.Errorf("watermark = %v, want the second night's last report", newest)
	}
}

func TestSynthesizeSleepStopsAtCoverageError(t *testing.T) {
	var records []maigo.Record
	boom := errors.New("boom")
	covered := func(start, end time.Time) (bool, error) {
		if start.After(at(40, 0)) {
			return false, boom
		}
		return false, nil
	}
	newest, err := synthesizeSleep(1, twoNights(), sql.NullTime{}, at(72, 0), covered, &records)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := countCategory(records, categoryInBed); n != 1 {
		t.Errorf("got %d in_bed records, want 1 (first night stands)", n)
	}
	if !newest.Valid || !newest.Time.Equal(at(31, 1)) {
		t.Errorf("watermark = %v, want to stop in front of the failed night", newest)
	}
}
