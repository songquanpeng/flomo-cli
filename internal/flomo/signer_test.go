package flomo

import "testing"

func TestSignIsStableAndSortsParameters(t *testing.T) {
	paramsA := map[string]any{"platform": "web", "timestamp": int64(123), "empty": "", "zero": 0}
	paramsB := map[string]any{"zero": 0, "empty": "", "timestamp": int64(123), "platform": "web"}
	gotA := sign(paramsA)
	gotB := sign(paramsB)
	if gotA != gotB {
		t.Fatalf("signature depends on map order: %q != %q", gotA, gotB)
	}
	if gotA != "8af1021f78bd6b5263d8246f236a32a3" {
		t.Fatalf("unexpected signature: %q", gotA)
	}
}
