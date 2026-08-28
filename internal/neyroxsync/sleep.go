package neyroxsync

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db/models"
	neyroxclient "github.com/tikhonp/medsenger-neyrox-bot/internal/util/neyrox_client"
)

// Medsenger sleep categories. All six are of type time_interval: their value is a
// time period written as "unixSeconds,unixSeconds" (see intervalValue).
const (
	categoryInBed             = "in_bed"
	categoryAwake             = "awake"
	categoryAsleepCore        = "asleep_core"
	categoryAsleepDeep        = "asleep_deep"
	categoryAsleepREM         = "asleep_REM"
	categoryAsleepUnspecified = "asleep_unspecified"
)

// hypnogramMetric is both the Neyrox endpoint and the watermark key for sleep.
//
// /api/v1/hypnogram/ is the only source of real sleep intervals. /api/v1/sleep/ is NOT
// usable for these categories: its rows are the running per-stage totals of the current
// session, in hours, re-reported every ~15 minutes — verified against the live API, e.g.
// one night reporting deep sleep as 0.2 → 0.5 → 0.8 → … → 2.5 h at successive
// timestamps. date_device is the moment of the report, not the start of a stage, so no
// row of it describes a time span; after waking the values fluctuate as summaries are
// re-sent. Turning that into intervals would mean inventing when each stage happened.
const hypnogramMetric = "hypnogram"

// sleepStageMatches maps a hypnogram element's stage label to a Medsenger category by
// substring. Labels come through in Russian; the English spellings are accepted too
// because the firmware is not consistent about which it sends.
var sleepStageMatches = []struct {
	substrings []string
	category   string
}{
	{[]string{"глубок", "deep"}, categoryAsleepDeep},
	{[]string{"поверхностн", "легк", "light", "core"}, categoryAsleepCore},
	{[]string{"быстр", "бдг", "парадоксальн", "rem"}, categoryAsleepREM},
	{[]string{"бодрств", "пробужд", "awake", "wake"}, categoryAwake},
	{[]string{"в постел", "в кроват", "in bed", "in_bed"}, categoryInBed},
}

// sleepCategory maps a stage label to a Medsenger category. The bool reports whether
// the label was recognised: an unrecognised one still yields asleep_unspecified (the
// category Medsenger provides for exactly that case) but is worth logging, since it
// usually means the band sent a spelling — or a numeric depth code — not yet handled.
func sleepCategory(label string) (string, bool) {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(label)), "ё", "е")
	if normalized == "" {
		return categoryAsleepUnspecified, false
	}
	for _, m := range sleepStageMatches {
		for _, s := range m.substrings {
			if strings.Contains(normalized, s) {
				return m.category, true
			}
		}
	}
	return categoryAsleepUnspecified, false
}

// intervalValue renders a Medsenger time_interval value: the period's start and end as
// comma-separated Unix seconds.
func intervalValue(start, end time.Time) string {
	return fmt.Sprintf("%d,%d", start.Unix(), end.Unix())
}

// newIntervalRecord builds a time_interval record. The record's own timestamp is the
// start of the period.
func newIntervalRecord(category string, start, end time.Time) maigo.Record {
	return maigo.NewRecord(category, intervalValue(start, end), start)
}

// nightRecords turns one night's decoded hypnogram into Medsenger records: one per
// stage interval, plus a single in_bed record spanning the whole night (unless the band
// already reported in-bed as a stage of its own). It also returns the stage labels it
// did not recognise, for the caller to log.
func nightRecords(intervals []neyroxclient.StageInterval) ([]maigo.Record, []string) {
	if len(intervals) == 0 {
		return nil, nil
	}

	records := make([]maigo.Record, 0, len(intervals)+1)
	var unknown []string
	var haveInBed bool
	start, end := intervals[0].Start, intervals[0].End

	for _, iv := range intervals {
		if !iv.End.After(iv.Start) {
			continue
		}
		category, ok := sleepCategory(iv.Stage)
		if !ok {
			unknown = append(unknown, iv.Stage)
		}
		if category == categoryInBed {
			haveInBed = true
		}
		records = append(records, newIntervalRecord(category, iv.Start, iv.End))
		if iv.Start.Before(start) {
			start = iv.Start
		}
		if iv.End.After(end) {
			end = iv.End
		}
	}

	if len(records) > 0 && !haveInBed && end.After(start) {
		records = append(records, newIntervalRecord(categoryInBed, start, end))
	}
	return records, unknown
}

// appendSleep appends one account's new sleep intervals as Medsenger records.
//
// One hypnogram row is one night, already grouped into stage intervals. Returns the new
// watermark, plus the endpoint to blame if the error is non-nil.
func (s *Syncer) appendSleep(
	acc *models.NeyroxAccount, access string, watermark sql.NullTime, records *[]maigo.Record,
) (sql.NullTime, string, error) {
	var newest sql.NullTime

	// A hypnogram failure other than an expired token is not fatal: the scalar metrics
	// already collected this cycle must not be thrown away over one source.
	hypnograms, err := s.nc.FetchHypnograms(access, sinceTime(watermark))
	if errors.Is(err, neyroxclient.ErrUnauthorized) {
		return newest, hypnogramMetric, err
	}
	if err != nil {
		log.Printf("contract %d: fetch hypnograms: %v", acc.ContractID, err)
		return newest, hypnogramMetric, nil
	}

	var unknown []string
	for _, h := range hypnograms {
		if watermark.Valid && !h.DateDevice.After(watermark.Time) {
			continue
		}
		intervals, err := neyroxclient.ParseIntervals(h)
		if err != nil {
			// An unreadable night must not sink the cycle: log the shape and move on.
			log.Printf("contract %d: hypnogram %s: %v", acc.ContractID, h.ID, err)
			continue
		}
		nightRecs, unk := nightRecords(intervals)
		if len(nightRecs) == 0 {
			continue
		}
		*records = append(*records, nightRecs...)
		unknown = append(unknown, unk...)
		newest = laterOf(newest, h.DateDevice)
	}

	s.logUnknownStages(acc, unknown)
	return newest, hypnogramMetric, nil
}

// logUnknownStages reports stage labels that fell through to asleep_unspecified, once
// per cycle. They usually mean the band sent a spelling — or a numeric depth code — the
// mapping in sleepStageMatches does not cover yet.
func (s *Syncer) logUnknownStages(acc *models.NeyroxAccount, labels []string) {
	if len(labels) == 0 {
		return
	}
	seen := make(map[string]bool, len(labels))
	unique := make([]string, 0, len(labels))
	for _, l := range labels {
		if seen[l] {
			continue
		}
		seen[l] = true
		unique = append(unique, l)
	}
	log.Printf("contract %d: unmapped sleep stages sent as %s: %q",
		acc.ContractID, categoryAsleepUnspecified, unique)
}

func laterOf(newest sql.NullTime, t time.Time) sql.NullTime {
	if !newest.Valid || t.After(newest.Time) {
		return sql.NullTime{Valid: true, Time: t}
	}
	return newest
}
