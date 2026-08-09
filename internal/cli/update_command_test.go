package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestUpdateCommandUsesCanonicalGoInstallCommand(t *testing.T) {
	var output bytes.Buffer
	rt := &runtime{out: &output, errOut: &output}
	command := newUpdateCommand(rt)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, updateCommand) {
		t.Fatalf("update output = %q, want canonical command %q", got, updateCommand)
	}
}
