package main

import "testing"

func TestExportCmdRejectsFileWithCommit(t *testing.T) {
	cmd := exportCmd()
	cmd.SetArgs([]string{"--file", "x.json", "--commit"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when --file and --commit are both set, got nil")
	}
}
