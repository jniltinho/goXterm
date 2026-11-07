package cmd

import (
	"errors"
	"testing"
)

func TestResolveSession(t *testing.T) {
	list := []map[string]any{
		{"id": int64(1), "name": "web"},
		{"id": int64(2), "name": "DB-Prod"},
	}
	if s := resolveSession(list, "2"); s == nil || s["name"] != "DB-Prod" {
		t.Errorf("by id: got %v", s)
	}
	if s := resolveSession(list, "db-prod"); s == nil || asInt64(s["id"]) != 2 { // case-insensitive name
		t.Errorf("by name: got %v", s)
	}
	if s := resolveSession(list, "missing"); s != nil {
		t.Errorf("miss should be nil, got %v", s)
	}
	if s := resolveSession(list, "99"); s != nil {
		t.Errorf("unknown id should be nil, got %v", s)
	}
}

func TestArgValidatorsUseUsageCode(t *testing.T) {
	var e exitErr
	if err := noArgs(nil, []string{"extra"}); !errors.As(err, &e) || e.code != 2 {
		t.Errorf("noArgs with an argument must be exitErr{2}, got %v", err)
	}
	if err := noArgs(nil, nil); err != nil {
		t.Errorf("noArgs with no arguments must pass, got %v", err)
	}
	if err := exactArgs(1)(nil, []string{}); !errors.As(err, &e) || e.code != 2 {
		t.Errorf("exactArgs(1) with 0 args must be exitErr{2}, got %v", err)
	}
	if err := exactArgs(1)(nil, []string{"x"}); err != nil {
		t.Errorf("exactArgs(1) with 1 arg must pass, got %v", err)
	}
}

func TestCodedErrorsCarryExitCodes(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
	}{{fail("x"), 1}, {usage("x"), 2}, {locked("x"), 3}} {
		var e exitErr
		if !errors.As(c.err, &e) || e.code != c.code {
			t.Errorf("want exitErr code %d, got %v", c.code, c.err)
		}
	}
}

// TestCommandTree checks every offline subcommand is registered under the root.
func TestCommandTree(t *testing.T) {
	want := map[string][]string{
		"sessions": {"list", "show"},
		"folders":  {"list"},
		"tunnels":  {"list"},
	}
	for parent, subs := range want {
		pc, _, err := rootCmd.Find([]string{parent})
		if err != nil || pc.Name() != parent {
			t.Fatalf("command %q not registered: %v", parent, err)
		}
		for _, sub := range subs {
			if c, _, err := rootCmd.Find([]string{parent, sub}); err != nil || c.Name() != sub {
				t.Errorf("%s %s not registered: %v", parent, sub, err)
			}
		}
	}
}
