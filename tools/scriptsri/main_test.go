package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

const (
	scriptURL = "https://cdn.test/lib@1.0.0/lib.js"
	// sha384 of "lib".
	libHash = "sha384-lEHfl3kUjUcMX0q3Q/QrxnthyzhgOxuXlb6siePXXFm6vHL1KhhDKjViZbXbbjP7"
)

func serve(_ context.Context, url string) ([]byte, error) {
	if url != scriptURL {
		return nil, errors.New("unexpected url " + url)
	}
	return []byte("lib"), nil
}

func TestPinScripts(t *testing.T) {
	pinned := `<script src="` + scriptURL + `" integrity="` + libHash + `" crossorigin="anonymous"></script>`
	for _, tc := range []struct {
		name  string
		html  string
		stale bool
	}{
		{"missing", `<script src="` + scriptURL + `" crossorigin="anonymous"></script>`, true},
		{"stale", `<script src="` + scriptURL + `" integrity="sha384-old" crossorigin="anonymous"></script>`, true},
		{"current", pinned, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, stale, err := pinScripts(t.Context(), []byte("<p>\n"+tc.html+"\n</p>"), serve)
			if err != nil {
				t.Fatal(err)
			}
			if want := "<p>\n" + pinned + "\n</p>"; string(got) != want {
				t.Errorf("html = %s, want %s", got, want)
			}
			if wantStale := tc.stale; slices.Equal(stale, []string{scriptURL}) != wantStale {
				t.Errorf("stale = %v, want stale %v", stale, wantStale)
			}
		})
	}
}

func TestPinScriptsRequiresAScript(t *testing.T) {
	if _, _, err := pinScripts(t.Context(), []byte("<script>inline()</script>"), serve); !errors.Is(err, errNoScript) {
		t.Fatalf("err = %v, want %v", err, errNoScript)
	}
}
