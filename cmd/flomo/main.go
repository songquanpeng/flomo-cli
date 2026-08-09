package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/songquanpeng/flomo-cli/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Execute(buildVersion()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func buildVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
