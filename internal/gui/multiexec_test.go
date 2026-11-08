package gui

import (
	"reflect"
	"testing"
)

// Same cases as the Rust engine test multiexec_fans_out_only_when_enabled.
func TestMultiExecTargets(t *testing.T) {
	m := multiExec{selected: map[string]bool{"a": true, "b": true}}
	if got := m.targets("a"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("off: %v", got)
	}
	m.enabled = true
	if got := m.targets("a"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("selected tab fans out: %v", got)
	}
	if got := m.targets("c"); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("unselected tab gets only its own input: %v", got)
	}
}
