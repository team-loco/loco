package v1alpha1

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestValidateFiles(t *testing.T) {
	valid := map[string]string{
		"/otel-config.yml":         "file_format: \"1.0\"\n",
		"/etc/app/config.yml":      "",
		"/etc/app-extra/settings":  "a",
		"/etc/hostname.d/override": "b",
	}
	if err := ValidateFiles(valid); err != nil {
		t.Fatalf("valid files rejected: %v", err)
	}

	tooMany := map[string]string{}
	for i := range MaxFiles + 1 {
		tooMany["/config/"+strconv.Itoa(i)] = "x"
	}
	half := strings.Repeat("x", MaxFilesBytes/2)
	invalid := map[string]map[string]string{
		"relative path":         {"config.yml": "x"},
		"root":                  {"/": "x"},
		"parent segment":        {"/app/../etc/config": "x"},
		"trailing slash":        {"/app/": "x"},
		"double slash":          {"/app//config": "x"},
		"under proc":            {"/proc/self/config": "x"},
		"kubelet managed file":  {"/etc/resolv.conf": "x"},
		"directory of reserved": {"/etc": "x"},
		"nested in another":     {"/app": "x", "/app-b": "y", "/app/config": "z"},
		"too many":              tooMany,
		"too large":             {"/a": half, "/b": half + "x"},
	}
	for name, files := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateFiles(files); err == nil {
				t.Errorf("accepted %v", slices.Sorted(maps.Keys(files)))
			}
		})
	}
}
