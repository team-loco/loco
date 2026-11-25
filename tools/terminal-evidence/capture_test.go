//go:build darwin || linux

package main

import (
	"bufio"
	"fmt"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/require"
)

func testOptions(t *testing.T, script string) captureOptions {
	t.Helper()
	return captureOptions{
		out: t.TempDir(), name: "sample", theme: darkTheme, cols: 30, rows: 5,
		timeout: 3 * time.Second, command: []string{"/bin/sh", "-c", script},
	}
}

func TestCapturesBothThemesAndRendersGlyphs(t *testing.T) {
	opts := testOptions(t, "")
	opts.theme = bothThemes
	opts.command = []string{os.Args[0], "-test.run=^TestTerminalQueryFixture$", "--", "terminal-fixture"}
	require.NoError(t, captureScreens(t.Context(), opts, io.Discard))
	for _, name := range []string{darkTheme, lightTheme} {
		t.Run(name, func(t *testing.T) {
			file, err := os.Open(outputPath(opts, name))
			require.NoError(t, err)
			defer file.Close()
			img, err := png.Decode(file)
			require.NoError(t, err)
			bg := themeFor(name).background
			require.Equal(t, color.RGBAModel.Convert(bg), color.RGBAModel.Convert(img.At(0, 0)))
			// The fixture emits Ready with the default background and no decoration.
			// A broken renderer that paints only the background must fail this check.
			glyphs := false
			for y := padding; y < padding+cellHeight; y++ {
				for x := padding; x < min(img.Bounds().Max.X, padding+5*16); x++ {
					if color.RGBAModel.Convert(img.At(x, y)) != color.RGBAModel.Convert(bg) {
						glyphs = true
					}
				}
			}
			require.True(t, glyphs, "PNG does not contain foreground glyph pixels for Ready")
		})
	}
}

func TestTerminalQueryFixture(t *testing.T) {
	if os.Args[len(os.Args)-1] != "terminal-fixture" {
		return
	}
	require.True(t, term.IsTerminal(os.Stdout.Fd()))
	_, err := term.MakeRaw(os.Stdin.Fd())
	require.NoError(t, err)
	_, err = fmt.Print("\x1b]11;?\x07\x1b[c")
	require.NoError(t, err)
	response, err := bufio.NewReader(os.Stdin).ReadString('c')
	require.NoError(t, err)
	want := "rgb:1414/1414/1414"
	if os.Getenv("COLORFGBG") == "0;15" {
		want = "rgb:ffff/ffff/ffff"
	}
	require.Contains(t, response, want)
	_, err = fmt.Print("Ready")
	require.NoError(t, err)
	os.Exit(0)
}

func TestInteractiveCapture(t *testing.T) {
	opts := testOptions(t, "printf 'Waiting for approval'; sleep 60")
	opts.until = "Waiting for approval"
	require.NoError(t, captureScreens(t.Context(), opts, io.Discard))
	require.FileExists(t, outputPath(opts, darkTheme))
	require.NoFileExists(t, outputPath(opts, lightTheme))
}

func TestMissingScreenFails(t *testing.T) {
	opts := testOptions(t, "printf 'different screen'")
	opts.until = "Waiting"
	err := captureScreens(t.Context(), opts, io.Discard)
	require.ErrorContains(t, err, "command exited before showing")
	require.NoFileExists(t, outputPath(opts, darkTheme))
}

func TestCompletedCommandExit(t *testing.T) {
	opts := testOptions(t, "printf 'error'; exit 3")
	opts.expectExit = 3
	require.NoError(t, captureScreens(t.Context(), opts, io.Discard))
	require.FileExists(t, outputPath(opts, darkTheme))

	opts.expectExit = 0
	require.ErrorContains(t, captureScreens(t.Context(), opts, io.Discard), "command exited 3, expected 0")
	require.NoFileExists(t, outputPath(opts, darkTheme), "failed retry retained a stale screenshot")
}

func TestTemporaryDirectoryRemoved(t *testing.T) {
	opts := testOptions(t, "printf '%s' \"$TMPDIR\" > \"$1\"; printf Ready; sleep 60")
	marker := filepath.Join(opts.out, "temp-dir.txt")
	opts.command = append(opts.command, "fixture", marker)
	opts.until = "Ready"
	require.NoError(t, captureScreens(t.Context(), opts, io.Discard))
	path, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.NoDirExists(t, string(path))
}

func TestStopsSurvivingDescendants(t *testing.T) {
	opts := testOptions(t, `
		(trap '' TERM HUP; printf armed > "$1"; sleep 0.5; printf leaked > "$2") &
		while [ ! -f "$1" ]; do sleep 0.01; done
		printf Ready
		sleep 60
	`)
	armed := filepath.Join(opts.out, "armed")
	leaked := filepath.Join(opts.out, "leaked")
	opts.command = append(opts.command, "fixture", armed, leaked)
	opts.until = "Ready"
	require.NoError(t, captureScreens(t.Context(), opts, io.Discard))
	require.FileExists(t, armed)
	time.Sleep(600 * time.Millisecond)
	require.NoFileExists(t, leaked, "the captured command left a worker running")
}

func TestTimeoutStopsChild(t *testing.T) {
	opts := testOptions(t, "sleep 60")
	opts.timeout = 300 * time.Millisecond
	require.ErrorContains(t, captureScreens(t.Context(), opts, io.Discard), "deadline exceeded")
	require.NoFileExists(t, outputPath(opts, darkTheme))
}

func TestCompletedScreenShowsExpectedText(t *testing.T) {
	opts := testOptions(t, "printf '\\033[38;2;12;34;56mReady\\033[0m'")
	theme := themeFor(darkTheme)
	em, err := capture(t.Context(), opts, theme)
	require.NoError(t, err)
	require.Contains(t, em.String(), "Ready")
	require.Equal(t, color.RGBA{12, 34, 56, 255}, color.RGBAModel.Convert(em.CellAt(0, 0).Style.Fg))
}
