package ocilayout

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/team-loco/loco/internal/workloadplatform"
)

const maxIndexBytes = 4 << 20

var (
	ErrTooLarge = errors.New("layout is too large")
	hexDigest   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func Validate(dir string, maxBytes int64) (int64, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", dir, err)
	}
	defer root.Close()

	fsys := root.FS()
	var total int64
	err = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", name, walkErr)
		}
		mode := d.Type()
		if mode.IsDir() {
			return checkDir(name)
		}
		if !mode.IsRegular() {
			return fmt.Errorf("%s is not a regular file or directory", name)
		}
		if path.Dir(name) != "blobs/sha256" {
			return nil
		}
		size, blobErr := checkBlob(root, name)
		if blobErr != nil {
			return blobErr
		}
		total += size
		if maxBytes > 0 && total > maxBytes {
			return fmt.Errorf("blobs exceed %d bytes: %w", maxBytes, ErrTooLarge)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	info, err := root.Lstat("index.json")
	if err != nil {
		return 0, fmt.Errorf("index.json: %w", err)
	}
	if info.Size() > maxIndexBytes {
		return 0, fmt.Errorf("index.json is %d bytes: %w", info.Size(), ErrTooLarge)
	}
	return total, nil
}

func checkDir(name string) error {
	if name == "blobs" || path.Dir(name) != "blobs" {
		return nil
	}
	if name != "blobs/sha256" {
		return fmt.Errorf("unsupported blob algorithm directory %s", name)
	}
	return nil
}

func checkBlob(root *os.Root, name string) (int64, error) {
	want := path.Base(name)
	if !hexDigest.MatchString(want) {
		return 0, fmt.Errorf("blob %s is not named after a sha256 digest", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return 0, fmt.Errorf("open blob %s: %w", name, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, fmt.Errorf("read blob %s: %w", name, err)
	}
	sum := h.Sum(nil)
	got := hex.EncodeToString(sum)
	if got != want {
		return 0, fmt.Errorf("blob %s has digest sha256:%s", name, got)
	}
	return n, nil
}

// ValidatePlatform checks the image BuildKit produced before it can be published as a successful build.
func ValidatePlatform(dir string, platform workloadplatform.Platform) error {
	index, err := layout.ImageIndexFromPath(dir)
	if err != nil {
		return fmt.Errorf("read built image: %w", err)
	}
	supported, err := indexSupportsPlatform(index, platform)
	if err != nil {
		return err
	}
	if !supported {
		return fmt.Errorf("built image has no %s variant", platform)
	}
	return nil
}

func indexSupportsPlatform(index v1.ImageIndex, platform workloadplatform.Platform) (bool, error) {
	manifest, err := index.IndexManifest()
	if err != nil {
		return false, fmt.Errorf("read built image index: %w", err)
	}
	found := false
	for _, descriptor := range manifest.Manifests {
		if declared := descriptor.Platform; declared != nil &&
			(declared.OS != "linux" || declared.Architecture != platform.Architecture()) {
			continue
		}
		if descriptor.MediaType.IsIndex() {
			child, readErr := index.ImageIndex(descriptor.Digest)
			if readErr != nil {
				return false, fmt.Errorf("read built child index: %w", readErr)
			}
			supported, checkErr := indexSupportsPlatform(child, platform)
			if checkErr != nil {
				return false, checkErr
			}
			if !supported {
				return false, fmt.Errorf("built child index has no %s variant", platform)
			}
			found = true
			continue
		}
		if !descriptor.MediaType.IsImage() {
			continue
		}
		image, readErr := index.Image(descriptor.Digest)
		if readErr != nil {
			return false, fmt.Errorf("read built image manifest: %w", readErr)
		}
		config, configErr := image.ConfigFile()
		if configErr != nil {
			return false, fmt.Errorf("read built image configuration: %w", configErr)
		}
		if config.OS != "linux" || config.Architecture != platform.Architecture() {
			return false, fmt.Errorf("built image targets %s/%s, expected %s", config.OS, config.Architecture, platform)
		}
		found = true
	}
	return found, nil
}
