package service

import (
	"testing"
	"time"
)

func TestWebTimezoneMatchesFlomoWebProtocol(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		zone *time.Location
		want string
	}{
		{"east eight", time.FixedZone("east", 8*3600), "8:0"},
		{"east five thirty", time.FixedZone("east", 5*3600+30*60), "5:30"},
		{"west five thirty", time.FixedZone("west", -(5*3600 + 30*60)), "-5:30"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := webTimezone(tt.zone, at); got != tt.want {
				t.Fatalf("webTimezone() = %q, want %q", got, tt.want)
			}
		})
	}
}
