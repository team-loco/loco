package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"time"
)

type Download struct {
	URL      string
	Timeout  time.Duration
	MaxBytes int64
}

type cappedReader struct {
	r         io.Reader
	remaining int64
	limit     int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		var probe [1]byte
		n, err := c.r.Read(probe[:])
		if n > 0 {
			return 0, fmt.Errorf("download is larger than %d bytes: %w", c.limit, ErrLimitExceeded)
		}
		return 0, err
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	return n, err
}

func Fetch(ctx context.Context, source Download, dest string, limits Limits) error {
	ctx, cancel := context.WithTimeout(ctx, source.Timeout)
	defer cancel()
	maxDownload := source.MaxBytes
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return fmt.Errorf("build source request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download source: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download source: unexpected status %s", resp.Status)
	}
	if maxDownload > 0 && resp.ContentLength > maxDownload {
		return fmt.Errorf(
			"source is %d bytes, over the %d byte limit: %w",
			resp.ContentLength,
			maxDownload,
			ErrLimitExceeded,
		)
	}
	var body io.Reader = resp.Body
	if maxDownload > 0 {
		body = &cappedReader{r: resp.Body, remaining: maxDownload, limit: maxDownload}
	}
	return Archive(body, dest, limits)
}

func CheckDockerfile(dest, dockerfile string) error {
	name, err := cleanName(dockerfile)
	if err != nil {
		return fmt.Errorf("dockerfile path: %w", err)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("open %s: %w", dest, err)
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no Dockerfile at %s in the build context", dockerfile)
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", dockerfile, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s in the build context is not a regular file", dockerfile)
	}
	return nil
}
