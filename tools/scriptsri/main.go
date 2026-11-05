// Command scriptsri checks that every external script in an HTML file carries
// the sha384 integrity hash of the file its URL serves, or rewrites the
// hashes with -write. Renovate moves the version in a script URL but cannot
// recompute the hash, so the check fails a bump until -write runs.
package main

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"time"
)

const fetchTimeout = time.Minute

var (
	errUsage    = errors.New("usage: scriptsri [-write] FILE")
	errNoScript = errors.New("no external script")

	scriptTag = regexp.MustCompile(`<script src="(https://[^"]+)"(?: integrity="([^"]*)")?`)
)

type fetchFunc func(ctx context.Context, url string) ([]byte, error)

// pinScripts returns html with each external script's integrity set to the
// hash fetch reports, and the URLs whose integrity was missing or different.
func pinScripts(ctx context.Context, html []byte, fetch fetchFunc) ([]byte, []string, error) {
	matches := scriptTag.FindAllSubmatchIndex(html, -1)
	if len(matches) == 0 {
		return nil, nil, errNoScript
	}
	var out bytes.Buffer
	var stale []string
	last := 0
	for _, m := range matches {
		url := string(html[m[2]:m[3]])
		body, err := fetch(ctx, url)
		if err != nil {
			return nil, nil, err
		}
		sum := sha512.Sum384(body)
		want := "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
		if m[4] < 0 || string(html[m[4]:m[5]]) != want {
			stale = append(stale, url)
		}
		out.Write(html[last:m[0]])
		fmt.Fprintf(&out, `<script src="%s" integrity="%s"`, url, want)
		last = m[1]
	}
	out.Write(html[last:])
	return out.Bytes(), stale, nil
}

func fetchURL(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("scriptsri", flag.ContinueOnError)
	write := flags.Bool("write", false, "rewrite the integrity hashes instead of checking them")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		return errUsage
	}
	path := flags.Arg(0)
	html, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	pinned, stale, err := pinScripts(ctx, html, fetchURL)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if *write {
		return os.WriteFile(path, pinned, 0o600) //nolint:gosec // the caller names the file
	}
	if len(stale) > 0 {
		return fmt.Errorf("%s: integrity is missing or stale for %v; run mise run fix:sri", path, stale)
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
