---
name: terminal-evidence
description: Capture before and after screenshots of real CLI output in light and dark terminal themes, including interactive prompts, spinners, help, and error states. Use for visible CLI changes and when asked for terminal screenshots for a PR. For web UI screenshots use ci-evidence instead.
---

# Terminal evidence

Capture a running CLI in a pseudo-terminal (PTY), not a re-created transcript or a screenshot of
the agent's own terminal. The helper runs the command in each theme, responds to terminal
color and device queries, and renders the screen's ANSI colors and styles into PNGs.
It does not forward clipboard escape sequences to the host clipboard.

## Capture

1. Use separate worktrees for the before and after refs. Build their actual CLIs into a scratch
   directory outside every checkout. Use the same synthetic fixture, arguments, dimensions,
   and terminal state for both refs. Do not use live authentication codes, credentials or
   production state. Commands run twice by default, once per theme.
2. From the repository root, run the helper with the pinned Python environment:

   ```sh
   evidence=$(mktemp -d)
   capture="$PWD/.agents/skills/terminal-evidence/scripts/capture.py"
   mise exec -- uv run --script "$capture" --out "$evidence" --name update-after \
     --rows 16 -- /absolute/path/to/locoadm update --help
   ```

   The output is `update-after-dark.png` and `update-after-light.png`. Set `--name` to a distinct
   before/after name for each state. Default dimensions are 88 columns by 16 rows. Choose
   dimensions that contain all relevant output and use the same dimensions in the comparison;
   the helper captures the visible viewport, not scrollback. For a new command, an after
   capture alone can be enough.

   For an interactive screen that does not exit, pass `--until 'literal visible text'`
   (prefer the last line that identifies the desired state). The helper captures the first
   matching frame and stops the process. It fails if the text never appears or the command
   exits first. For a command that exits with an expected error, pass `--expect-exit 1`
   or its actual exit code. Both modes fail after `--timeout` seconds (15 by default).
   Use `--theme light` or `--theme dark` for a single-theme retry.

   The renderer uses Menlo and Apple Symbols on macOS, and DejaVu Sans Mono on Linux.
   This is a headless terminal emulator, so check the images for clipping, missing glyphs or
   unsupported terminal controls before publishing. Do not use its screenshot as proof that
   an operating-system clipboard write succeeded.

## Example: device login

The included fixture runs the real `loco login --device` command with the CLI test suite's
local fake API, a temporary home directory, and the synthetic code `ABCD-EFGH`. Build each
ref's fixture into a separate binary. The build helper overlays the test file without
changing either checkout.

```sh
skill="$PWD/.agents/skills/terminal-evidence"
"$skill/scripts/build-device-login.sh" /absolute/path/to/before-worktree "$evidence/before"
"$skill/scripts/build-device-login.sh" /absolute/path/to/after-worktree "$evidence/after"

mise exec -- uv run --script "$skill/scripts/capture.py" --out "$evidence" --name login-before \
  --rows 8 --until "Press 'q' or Ctrl+C to quit" -- \
  "$evidence/before" '-test.run=^TestCaptureDeviceLogin$'
mise exec -- uv run --script "$skill/scripts/capture.py" --out "$evidence" --name login-after \
  --rows 8 --until "Press 'q' or Ctrl+C to quit" -- \
  "$evidence/after" '-test.run=^TestCaptureDeviceLogin$'
```

For other interactive workflows, use a real CLI binary with a local test fixture, or adapt the
[device-login fixture](examples/device_login_test.go.txt) to the CLI's existing test API.

## Attach to the PR

Open and inspect every PNG. Keep existing PR text and replace any stale screenshots. Add a
`## Terminal screenshots` section to the body file in the scratch directory:

```markdown
| Theme | Before | After |
| --- | --- | --- |
| Dark | ![Before, dark terminal](./login-before-dark.png) | ![After, dark terminal](./login-after-dark.png) |
| Light | ![Before, light terminal](./login-before-light.png) | ![After, light terminal](./login-after-light.png) |
```

Upload all referenced PNGs:

```sh
gh pr edit PR_NUMBER --body-file "$evidence/body.md" \
  --attach "$evidence/login-before-dark.png" --attach "$evidence/login-after-dark.png" \
  --attach "$evidence/login-before-light.png" --attach "$evidence/login-after-light.png"
```

Read the PR body back with `gh pr view PR_NUMBER --json body` and check every PNG reference
was rewritten to an uploaded asset URL. Never commit the PNGs.

## Verify the helper

Run the black-box PTY tests after changing the capture script:

```sh
mise exec -- python .agents/skills/terminal-evidence/scripts/test_capture.py
```
