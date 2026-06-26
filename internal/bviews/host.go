// Package bviews holds the shared base layout and view-wide configuration.
package bviews

// Host is the public scheme+host of this agent, set at startup from config.
// Views use it to build absolute URLs.
var Host = "uninitialized"
