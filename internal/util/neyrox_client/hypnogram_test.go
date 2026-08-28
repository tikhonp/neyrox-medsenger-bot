package neyroxclient

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func night(t *testing.T, data string) Hypnogram {
	t.Helper()
	start, err := time.Parse(time.RFC3339, "2026-08-27T23:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return Hypnogram{ID: "n1", DateDevice: start, Data: json.RawMessage(data)}
}

func TestParseIntervalsDurations(t *testing.T) {
	tests := []struct {
		name string
		data string
		want []string // "stage start..end", times as RFC3339
	}{
		{
			// Firmware 2.7.41 per the OpenAPI note.
			name: "time_long_sec laid end to end",
			data: `[{"stage":"Глубокий сон","time_long_sec":3600},{"stage":"Поверхностный сон","time_long_sec":1800}]`,
			want: []string{
				"Глубокий сон 2026-08-27T23:00:00Z..2026-08-28T00:00:00Z",
				"Поверхностный сон 2026-08-28T00:00:00Z..2026-08-28T00:30:00Z",
			},
		},
		{
			// Firmware 2.7.40 per the OpenAPI note.
			name: "time_long_min laid end to end",
			data: `[{"type":"deep","time_long_min":60},{"type":"rem","time_long_min":30}]`,
			want: []string{
				"deep 2026-08-27T23:00:00Z..2026-08-28T00:00:00Z",
				"rem 2026-08-28T00:00:00Z..2026-08-28T00:30:00Z",
			},
		},
		{
			name: "absolute timestamps win over the cursor",
			data: `[{"stage":"awake","start":"2026-08-28T01:00:00Z","end":"2026-08-28T01:15:00Z"}]`,
			want: []string{"awake 2026-08-28T01:00:00Z..2026-08-28T01:15:00Z"},
		},
		{
			name: "unix seconds",
			data: `[{"stage":"deep","start":1787871600,"end":1787875200}]`,
			want: []string{"deep 2026-08-27T23:00:00Z..2026-08-28T00:00:00Z"},
		},
		{
			name: "array wrapped in an object",
			data: `{"intervals":[{"depth":"Глубокий сон","time_long_sec":600}]}`,
			want: []string{"Глубокий сон 2026-08-27T23:00:00Z..2026-08-27T23:10:00Z"},
		},
		{
			name: "numeric depth code kept as a label",
			data: `[{"depth":3,"time_long_sec":600}]`,
			want: []string{"3 2026-08-27T23:00:00Z..2026-08-27T23:10:00Z"},
		},
		{
			name: "zero-length intervals dropped",
			data: `[{"stage":"deep","time_long_sec":0},{"stage":"rem","time_long_sec":600}]`,
			want: []string{"rem 2026-08-27T23:00:00Z..2026-08-27T23:10:00Z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIntervals(night(t, tt.data))
			if err != nil {
				t.Fatalf("ParseIntervals: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d intervals, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, iv := range got {
				s := iv.Stage + " " + iv.Start.UTC().Format(time.RFC3339) + ".." + iv.End.UTC().Format(time.RFC3339)
				if s != tt.want[i] {
					t.Errorf("interval %d = %q, want %q", i, s, tt.want[i])
				}
			}
		})
	}
}

func TestParseIntervalsEmpty(t *testing.T) {
	for _, data := range []string{"", "null", "[]"} {
		got, err := ParseIntervals(night(t, data))
		if err != nil || got != nil {
			t.Errorf("data %q: got (%v, %v), want (nil, nil)", data, got, err)
		}
	}
}

func TestParseIntervalsUnknownShape(t *testing.T) {
	_, err := ParseIntervals(night(t, `[{"foo":1,"bar":2}]`))
	var shapeErr *ErrUnknownHypnogramShape
	if !errors.As(err, &shapeErr) {
		t.Fatalf("got %v, want ErrUnknownHypnogramShape", err)
	}
	if len(shapeErr.Keys) != 2 || shapeErr.Keys[0] != "bar" || shapeErr.Keys[1] != "foo" {
		t.Errorf("keys = %v, want [bar foo]", shapeErr.Keys)
	}
}

// The shape the tracker actually sends, per the typeindicators row
// "Гипнограмма сна (массив интервалов level/time/timestamp)".
func TestParseIntervalsLevelTimeTimestamp(t *testing.T) {
	// Consecutive timestamps are 20 and 40 minutes apart while `time` reads 20 and 40,
	// so the unit must be inferred as minutes.
	data := `[{"level":2,"time":20,"timestamp":1787871600},
	          {"level":3,"time":40,"timestamp":1787872800},
	          {"level":1,"time":15,"timestamp":1787875200}]`
	got, err := ParseIntervals(night(t, data))
	if err != nil {
		t.Fatalf("ParseIntervals: %v", err)
	}
	want := []string{
		"2 2026-08-27T23:00:00Z..2026-08-27T23:20:00Z",
		"3 2026-08-27T23:20:00Z..2026-08-28T00:00:00Z",
		"1 2026-08-28T00:00:00Z..2026-08-28T00:15:00Z",
	}
	assertIntervals(t, got, want)
}

func TestParseIntervalsInfersSeconds(t *testing.T) {
	// Same timestamps, but `time` now reads in seconds: 1200 and 2400.
	data := `[{"level":2,"time":1200,"timestamp":1787871600},
	          {"level":3,"time":2400,"timestamp":1787872800}]`
	got, err := ParseIntervals(night(t, data))
	if err != nil {
		t.Fatalf("ParseIntervals: %v", err)
	}
	assertIntervals(t, got, []string{
		"2 2026-08-27T23:00:00Z..2026-08-27T23:20:00Z",
		"3 2026-08-27T23:20:00Z..2026-08-28T00:00:00Z",
	})
}

func TestParseIntervalsFallsBackToNextStart(t *testing.T) {
	// No duration at all: a stage runs until the next one begins.
	data := `[{"level":2,"timestamp":1787871600},{"level":3,"timestamp":1787875200,"time":15}]`
	got, err := ParseIntervals(night(t, data))
	if err != nil {
		t.Fatalf("ParseIntervals: %v", err)
	}
	assertIntervals(t, got, []string{
		"2 2026-08-27T23:00:00Z..2026-08-28T00:00:00Z",
		"3 2026-08-28T00:00:00Z..2026-08-28T00:15:00Z",
	})
}

func assertIntervals(t *testing.T, got []StageInterval, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d intervals, want %d: %+v", len(got), len(want), got)
	}
	for i, iv := range got {
		s := iv.Stage + " " + iv.Start.UTC().Format(time.RFC3339) + ".." + iv.End.UTC().Format(time.RFC3339)
		if s != want[i] {
			t.Errorf("interval %d = %q, want %q", i, s, want[i])
		}
	}
}
