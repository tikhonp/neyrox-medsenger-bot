package neyroxclient

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrUnknownHypnogramShape is returned when a hypnogram's data array holds elements
// this decoder cannot read. It carries the keys that were actually seen so the shape
// can be pinned from the log line instead of guessed.
type ErrUnknownHypnogramShape struct {
	Keys []string
}

func (e *ErrUnknownHypnogramShape) Error() string {
	return fmt.Sprintf("neyrox: unrecognised hypnogram element, keys seen: %s", strings.Join(e.Keys, ", "))
}

// Key sets accepted inside one hypnogram data element.
//
// The OpenAPI schema types `data` only as `oneOf: [{}, null]`, but the typeindicators
// row names the shape: "Гипнограмма сна (массив интервалов level/time/timestamp)" — so
// an element is {level, time, timestamp}. The other spellings are kept because the two
// firmware generations the spec calls out ("Прошивка 2.7.40 — time_long_min, 2.7.41 —
// time_long_sec") disagree about the duration key, and only those two carry their unit
// in the name.
var (
	stageKeys       = []string{"level", "stage", "type", "depth", "state", "phase", "sleep_stage", "name"}
	durationSecKeys = []string{"time_long_sec", "duration_sec", "seconds", "sec"}
	durationMinKeys = []string{"time_long_min", "duration_min", "minutes", "min"}
	durationAnyKeys = []string{"time", "time_long", "duration", "length", "value"}
	startKeys       = []string{"timestamp", "start", "date_start", "time_start", "date_from", "begin", "from"}
	endKeys         = []string{"end", "date_end", "time_end", "date_to", "finish", "to"}
)

// element is one decoded hypnogram entry before its span is resolved.
type element struct {
	stage string
	start time.Time
	end   time.Time
	// rawDuration is the duration as reported; unit is its scale, or 0 when the key
	// that carried it does not name a unit.
	rawDuration float64
	unit        time.Duration
	hasStart    bool
	hasEnd      bool
	hasDuration bool
	keys        []string
}

// ParseIntervals decodes one night's hypnogram into stage intervals.
//
// An element's span is resolved from whatever it carries, in descending order of trust:
// an explicit end, a duration, or the start of the element after it (a hypnogram is a
// contiguous walk through the night, so the next entry begins where this one stops).
// Elements with no start of their own are laid end to end from h.DateDevice.
// Returns (nil, nil) for an empty or null data array.
func ParseIntervals(h Hypnogram) ([]StageInterval, error) {
	raw, err := hypnogramElements(h.Data)
	if err != nil || len(raw) == 0 {
		return nil, err
	}

	elements := make([]element, 0, len(raw))
	for _, r := range raw {
		elements = append(elements, decodeElement(r))
	}
	unit := durationUnit(elements)

	intervals := make([]StageInterval, 0, len(elements))
	cursor := h.DateDevice
	var unknownKeys []string

	for i, el := range elements {
		start := cursor
		if el.hasStart {
			start = el.start
		}

		var end time.Time
		switch {
		case el.hasEnd:
			end = el.end
		case el.hasDuration:
			scale := el.unit
			if scale == 0 {
				scale = unit
			}
			end = start.Add(time.Duration(el.rawDuration * float64(scale)))
		case i+1 < len(elements) && elements[i+1].hasStart:
			end = elements[i+1].start
		default:
			if unknownKeys == nil {
				unknownKeys = el.keys
			}
			continue
		}

		if !end.After(start) {
			continue
		}
		intervals = append(intervals, StageInterval{Stage: el.stage, Start: start, End: end})
		cursor = end
	}

	if len(intervals) == 0 {
		if unknownKeys != nil {
			return nil, &ErrUnknownHypnogramShape{Keys: unknownKeys}
		}
		return nil, nil
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].Start.Before(intervals[j].Start) })
	return intervals, nil
}

func decodeElement(m map[string]any) element {
	el := element{stage: pickStage(m), keys: elementKeys(m)}
	el.start, el.hasStart = pickTime(m, startKeys)
	el.end, el.hasEnd = pickTime(m, endKeys)
	el.rawDuration, el.unit, el.hasDuration = pickDuration(m)
	return el
}

// durationUnit resolves the scale of a duration read from a key that does not name one
// (`time`, the key the tracker actually sends).
//
// Consecutive elements carry absolute timestamps, so the gap between two of them is the
// duration of the first in real seconds: comparing that gap against the reported number
// says whether the number is seconds or minutes. Falls back to minutes, the only unit
// the Hypnogram schema itself states ("Суммарная длительность сна, мин").
func durationUnit(elements []element) time.Duration {
	var seconds, minutes int
	for i, el := range elements {
		if el.unit != 0 || !el.hasDuration || el.rawDuration <= 0 || !el.hasStart {
			continue
		}
		if i+1 >= len(elements) || !elements[i+1].hasStart {
			continue
		}
		gap := elements[i+1].start.Sub(el.start).Seconds()
		if gap <= 0 {
			continue
		}
		// Which unit explains the gap better?
		if absDiff(gap, el.rawDuration) < absDiff(gap, el.rawDuration*60) {
			seconds++
		} else {
			minutes++
		}
	}
	if seconds > minutes {
		return time.Second
	}
	return time.Minute
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

// hypnogramElements unwraps the data field into a list of objects, accepting both a
// bare array and an object wrapping one under any key.
func hypnogramElements(data json.RawMessage) ([]map[string]any, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}

	var elements []map[string]any
	if err := json.Unmarshal(data, &elements); err == nil {
		return elements, nil
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("neyrox: decode hypnogram data: %w", err)
	}
	for _, v := range wrapper {
		if err := json.Unmarshal(v, &elements); err == nil && len(elements) > 0 {
			return elements, nil
		}
	}
	return nil, &ErrUnknownHypnogramShape{Keys: mapKeys(wrapper)}
}

func pickStage(el map[string]any) string {
	for _, k := range stageKeys {
		v, ok := el[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
		case float64:
			// A numeric depth code; the mapping layer resolves it by value.
			return strconv.FormatFloat(t, 'f', -1, 64)
		}
	}
	return ""
}

// pickDuration returns the reported duration, the unit its key names (0 when the key
// names none), and whether one was found at all.
func pickDuration(el map[string]any) (float64, time.Duration, bool) {
	if v, ok := pickFloat(el, durationSecKeys); ok {
		return v, time.Second, true
	}
	if v, ok := pickFloat(el, durationMinKeys); ok {
		return v, time.Minute, true
	}
	if v, ok := pickFloat(el, durationAnyKeys); ok {
		return v, 0, true
	}
	return 0, 0, false
}

func pickFloat(el map[string]any, keys []string) (float64, bool) {
	for _, k := range keys {
		v, ok := el[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			return t, true
		case string:
			if f, err := strconv.ParseFloat(t, 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

// timeLayouts are the timestamp spellings accepted inside a hypnogram element.
var timeLayouts = []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"}

func pickTime(el map[string]any, keys []string) (time.Time, bool) {
	for _, k := range keys {
		v, ok := el[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			for _, layout := range timeLayouts {
				if parsed, err := time.Parse(layout, t); err == nil {
					return parsed, true
				}
			}
		case float64:
			// A Unix timestamp; values far past the second range are milliseconds.
			if t > 1e11 {
				return time.UnixMilli(int64(t)).UTC(), true
			}
			if t > 1e9 {
				return time.Unix(int64(t), 0).UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func elementKeys(el map[string]any) []string {
	keys := make([]string, 0, len(el))
	for k := range el {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
