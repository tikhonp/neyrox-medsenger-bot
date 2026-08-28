package neyroxclient

import (
	"encoding/json"
	"time"
)

// TokenPair is the response of POST /api/token/.
type TokenPair struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
}

// Measurement is one record from a Neyrox /api/v1/<metric>/ endpoint.
// All smart-band metric serializers share this core shape (see the OpenAPI spec
// at https://adm.neyrox.com/api/schema/).
type Measurement struct {
	ID            string    `json:"id"`
	Value         *float64  `json:"value"`
	TypeIndicator string    `json:"type_indicator"`
	DateDevice    time.Time `json:"date_device"`
	CreatedAt     time.Time `json:"created_at"`
}

// paginatedMeasurements matches DRF's default pagination wrapper.
type paginatedMeasurements struct {
	Count   int           `json:"count"`
	Next    *string       `json:"next"`
	Results []Measurement `json:"results"`
}

// TypeIndicator is one row of the Neyrox /api/v1/typeindicators/ reference table.
// Measurements reference it by ID via their type_indicator field; the Name (e.g.
// "Систолическое давление") is what lets us tell systolic from diastolic.
type TypeIndicator struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

// paginatedTypeIndicators matches DRF's default pagination wrapper for typeindicators.
type paginatedTypeIndicators struct {
	Next    *string         `json:"next"`
	Results []TypeIndicator `json:"results"`
}

// Hypnogram is one night's sleep hypnogram from /api/v1/hypnogram/ — "Гипнограмма
// одной ночи (массив интервалов глубины сна)".
//
// Data is left raw because the OpenAPI schema declares it only as `oneOf: [{}, null]`.
// Its element shape is firmware-dependent (the spec's own note: "Прошивка 2.7.40 —
// time_long_min, 2.7.41 — time_long_sec"), so it is decoded by ParseIntervals.
type Hypnogram struct {
	ID            string          `json:"id"`
	TypeIndicator string          `json:"type_indicator"`
	Data          json.RawMessage `json:"data"`
	TotalMinutes  *int            `json:"total_minutes"`
	DateDevice    time.Time       `json:"date_device"`
	CreatedAt     time.Time       `json:"created_at"`
}

// paginatedHypnograms matches DRF's default pagination wrapper for hypnograms.
type paginatedHypnograms struct {
	Next    *string     `json:"next"`
	Results []Hypnogram `json:"results"`
}

// StageInterval is one decoded hypnogram element: a sleep stage over a time span.
// Stage is the raw label as Neyrox reports it; mapping it to a Medsenger category is
// the caller's job.
type StageInterval struct {
	Stage string
	Start time.Time
	End   time.Time
}
