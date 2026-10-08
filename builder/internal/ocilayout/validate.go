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
