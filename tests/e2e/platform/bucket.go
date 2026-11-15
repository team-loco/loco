package platform

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// S3 runs the AWS CLI against the source bucket from the kind network, with dir mounted at /work.
func (p *Platform) S3(ctx context.Context, dir string, args ...string) (string, error) {
	command := make([]string, 0, 17+len(args))
	command = append(command, "run", "--rm", "--network", "kind",
		"-e", "AWS_ACCESS_KEY_ID="+sourceAccessKey,
		"-e", "AWS_SECRET_ACCESS_KEY="+sourceSecretKey,
		"-e", "AWS_DEFAULT_REGION="+clusterRegion,
		"-e", "AWS_CONFIG_FILE=/work/aws-config",
		"-v", dir+":/work",
		fromImage(p.root, "tests/e2e/fixtures/aws-cli/Dockerfile"), "--endpoint-url", p.S3Endpoint)
	return p.Command(ctx, "docker", append(command, args...)...)
}

// UploadSource packs dir into the source bucket as sources/<name>.tar.gz and returns a presigned
// GET URL for it.
func (p *Platform) UploadSource(tb testing.TB, name, dir string) string {
	tb.Helper()
	work := tb.TempDir()
	if err := writeReadable(
		filepath.Join(work, "aws-config"),
		[]byte("[default]\ns3 =\n  addressing_style = path\n"),
	); err != nil {
		tb.Fatal(err)
	}
	archive := p.PackSource(tb, dir)
	if err := os.Rename(archive, filepath.Join(work, name+".tar.gz")); err != nil {
		tb.Fatal(err)
	}
	object := "s3://" + sourceBucket + "/sources/" + name + ".tar.gz"
	if _, err := p.S3(
		tb.Context(),
		work,
		"s3",
		"cp",
		"--only-show-errors",
		"/work/"+name+".tar.gz",
		object,
	); err != nil {
		tb.Fatal(err)
	}
	url, err := p.S3(tb.Context(), work, "s3", "presign", object, "--expires-in", "3600")
	if err != nil {
		tb.Fatal(err)
	}
	return strings.TrimSpace(url)
}

// PackSource packs dir into a gzipped tarball and returns its path.
func (p *Platform) PackSource(tb testing.TB, dir string) string {
	tb.Helper()
	archive := filepath.Join(tb.TempDir(), "source.tar.gz")
	if _, err := p.Command(
		tb.Context(),
		"env",
		"COPYFILE_DISABLE=1",
		"tar",
		"-czf",
		archive,
		"-C",
		dir,
		".",
	); err != nil {
		tb.Fatal(err)
	}
	return archive
}

// Upload PUTs a file to a presigned URL of the source bucket.
func Upload(tb testing.TB, url, path string) {
	tb.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	request, err := http.NewRequestWithContext(tb.Context(), http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		tb.Fatal(err)
	}
	response, err := HTTPClient.Do(request)
	if err != nil {
		tb.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, err := io.ReadAll(response.Body)
		tb.Fatalf("upload to the source bucket: HTTP %d: %s (%v)", response.StatusCode, body, err)
	}
}

// HTTPClient resolves every *.localhost name, such as the source bucket's, to the loopback address.
var HTTPClient = &http.Client{Transport: &http.Transport{
	DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(host, ".localhost") {
			address = net.JoinHostPort("127.0.0.1", port)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	},
}}

// SourceExists reports whether the bucket holds the source of a build.
func (p *Platform) SourceExists(tb testing.TB, buildID string) bool {
	tb.Helper()
	_, err := p.S3(
		tb.Context(),
		tb.TempDir(),
		"s3api",
		"head-object",
		"--bucket",
		sourceBucket,
		"--key",
		"sources/"+buildID+".tar.gz",
	)
	if err != nil && !strings.Contains(err.Error(), "Not Found") {
		tb.Fatal(err)
	}
	return err == nil
}

// SourceBucketURL is the path-style URL prefix of the build sources.
func (p *Platform) SourceBucketURL() string {
	return p.S3Endpoint + "/" + sourceBucket + "/sources/"
}
