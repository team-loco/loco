package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHeadCLIActivation(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run(map[bool]string{true: "head ready", false: "missing head fails upgrade"}[present], func(t *testing.T) {
			dir := t.TempDir()
			active := filepath.Join(dir, locoadmExecutable)
			head := filepath.Join(dir, "locoadm-head")
			if err := os.WriteFile(active, []byte("baseline"), 0700); err != nil {
				t.Fatal(err)
			}
			if present {
				if err := os.WriteFile(head, []byte("head"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)
			err := activateHeadCLI()
			if (err == nil) != present {
				t.Fatalf("activate head = %v, head present %t", err, present)
			}
			data, err := os.ReadFile(active)
			want := "baseline"
			if present {
				want = "head"
			}
			if err != nil || string(data) != want {
				t.Fatalf("active CLI = %q, %v; want %s", data, err, want)
			}
		})
	}
}
