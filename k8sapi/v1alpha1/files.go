package v1alpha1

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

// The total keeps the Application and the files ConfigMap well under the 1 MiB object size limit.
const (
	MaxFiles      = 20
	MaxFilesBytes = 256 * 1024
)

// reservedFilePaths are paths the kernel or the kubelet provide in every container.
var reservedFilePaths = []string{"/proc", "/sys", "/dev", "/etc/hosts", "/etc/hostname", "/etc/resolv.conf"}

// ValidateFiles checks the paths, count and total size of the files a service mounts.
func ValidateFiles(files map[string]string) error {
	if len(files) > MaxFiles {
		return fmt.Errorf("too many files: %d (max %d)", len(files), MaxFiles)
	}
	total := 0
	for _, content := range files {
		total += len(content)
	}
	if total > MaxFilesBytes {
		return fmt.Errorf("files hold %d bytes (max %d)", total, MaxFilesBytes)
	}
	paths := slices.Sorted(maps.Keys(files))
	for i, p := range paths {
		if !path.IsAbs(p) || path.Clean(p) != p || p == "/" {
			return fmt.Errorf("file path %q must be an absolute, clean path to a file", p)
		}
		for _, reserved := range reservedFilePaths {
			if within(p, reserved) || within(reserved, p) {
				return fmt.Errorf("file path %q overlaps %s, which the container runtime provides", p, reserved)
			}
		}
		for _, earlier := range paths[:i] {
			if within(p, earlier) {
				return fmt.Errorf("file path %q is inside file path %q", p, earlier)
			}
		}
	}
	return nil
}

// within reports whether p is dir or a path below it.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+"/")
}
