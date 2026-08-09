package main

import "testing"

func TestExplicitVersionWins(t *testing.T) {
	original := version
	version = "v1.2.3"
	t.Cleanup(func() { version = original })
	if got := buildVersion(); got != "v1.2.3" {
		t.Fatalf("buildVersion() = %q, want v1.2.3", got)
	}
}
