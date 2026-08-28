package neyroxsync

import (
	"testing"
	"time"

	neyroxclient "github.com/tikhonp/medsenger-neyrox-bot/internal/util/neyrox_client"
)

func TestSleepCategory(t *testing.T) {
	tests := []struct {
		label     string
		want      string
		wantKnown bool
	}{
		{"Глубокий сон", categoryAsleepDeep, true},
		{"Поверхностный сон", categoryAsleepCore, true},
		{"Лёгкий сон", categoryAsleepCore, true}, // ё is normalised to е
		{"Легкий сон", categoryAsleepCore, true},
		{"Быстрый сон", categoryAsleepREM, true},
		{"БДГ-фаза", categoryAsleepREM, true},
		{"Бодрствование", categoryAwake, true},
		{"Пробуждение", categoryAwake, true},
		{"В постели", categoryInBed, true},
		{"Deep sleep", categoryAsleepDeep, true},
		{"REM", categoryAsleepREM, true},
		{"", categoryAsleepUnspecified, false},
		{"3", categoryAsleepUnspecified, false},
		{"Неведомая фаза", categoryAsleepUnspecified, false},
	}
	for _, tt := range tests {
		got, known := sleepCategory(tt.label)
		if got != tt.want || known != tt.wantKnown {
			t.Errorf("sleepCategory(%q) = (%q, %v), want (%q, %v)", tt.label, got, known, tt.want, tt.wantKnown)
		}
	}
}

func TestIntervalValue(t *testing.T) {
	start := time.Date(2026, 8, 27, 23, 0, 0, 0, time.UTC)
	got := intervalValue(start, start.Add(time.Hour))
	if want := "1787871600,1787875200"; got != want {
		t.Errorf("intervalValue = %q, want %q", got, want)
	}
}

func stage(label string, startHour, endHour int) neyroxclient.StageInterval {
	day := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	return neyroxclient.StageInterval{
		Stage: label,
		Start: day.Add(time.Duration(startHour) * time.Hour),
		End:   day.Add(time.Duration(endHour) * time.Hour),
	}
}

func TestNightRecordsSynthesisesInBed(t *testing.T) {
	records, unknown := nightRecords([]neyroxclient.StageInterval{
		stage("Глубокий сон", 1, 2),
		stage("Поверхностный сон", 2, 4),
	})
	if len(unknown) != 0 {
		t.Errorf("unknown = %v, want none", unknown)
	}
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3 (2 stages + in_bed)", len(records))
	}
	inBed := records[len(records)-1]
	if inBed.CategoryName != categoryInBed {
		t.Fatalf("last record is %q, want %q", inBed.CategoryName, categoryInBed)
	}
	// in_bed spans the whole night: 01:00 to 04:00.
	if want := intervalValue(stage("", 1, 4).Start, stage("", 1, 4).End); inBed.Value != want {
		t.Errorf("in_bed value = %v, want %v", inBed.Value, want)
	}
}

func TestNightRecordsKeepsReportedInBed(t *testing.T) {
	records, _ := nightRecords([]neyroxclient.StageInterval{
		stage("В постели", 0, 5),
		stage("Глубокий сон", 1, 2),
	})
	var inBed int
	for _, r := range records {
		if r.CategoryName == categoryInBed {
			inBed++
		}
	}
	if inBed != 1 {
		t.Errorf("got %d in_bed records, want 1 (no synthesised duplicate)", inBed)
	}
}

func TestNightRecordsReportsUnknownStages(t *testing.T) {
	records, unknown := nightRecords([]neyroxclient.StageInterval{stage("Неведомая фаза", 1, 2)})
	if len(unknown) != 1 || unknown[0] != "Неведомая фаза" {
		t.Errorf("unknown = %v, want [Неведомая фаза]", unknown)
	}
	if records[0].CategoryName != categoryAsleepUnspecified {
		t.Errorf("category = %q, want %q", records[0].CategoryName, categoryAsleepUnspecified)
	}
}
