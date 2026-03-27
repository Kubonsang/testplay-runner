package main

import (
	"io"

	"github.com/spf13/cobra"
)

// version is the current release string.
// Override at build time with:
//
//	go build -ldflags="-X main.version=v0.1.0-beta+abc1234"
var version = "v0.1.0-beta"

// commit and date are injected by the release build pipeline.
// Left empty in development builds.
var (
	commit = ""
	date   = ""
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print fastplay version as JSON",
	Args:  cobra.NoArgs,
	Run:   func(cmd *cobra.Command, args []string) { runVersion(cmd.OutOrStdout()) },
}

func runVersion(w io.Writer) {
	out := map[string]any{
		"version": version,
	}
	if commit != "" {
		out["commit"] = commit
	}
	if date != "" {
		out["date"] = date
	}
	writeJSON(w, out)
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
