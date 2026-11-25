package cli

import "testing"

func TestCLIBasicAndPositionalArgRejection(t *testing.T) {
	if code := Run(nil); code != 2 {
		t.Fatalf("expected exit 2 with no args, got %d", code)
	}
	if code := Run([]string{"version"}); code != 0 {
		t.Fatalf("expected exit 0 for version, got %d", code)
	}
	if code := Run([]string{"unknown"}); code != 2 {
		t.Fatalf("expected exit 2 for unknown command, got %d", code)
	}
	if code := Run([]string{"validate", "--spec", "foo.yaml", "extra"}); code != 2 {
		t.Fatalf("expected exit 2 for unexpected positional arg in validate, got %d", code)
	}
	if code := Run([]string{"activate", "extra"}); code != 2 {
		t.Fatalf("expected exit 2 for unexpected positional arg in activate, got %d", code)
	}
	if code := Run([]string{"run", "--spec", "foo.yaml", "extra"}); code != 2 {
		t.Fatalf("expected exit 2 for unexpected positional arg in run, got %d", code)
	}
}
