#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.15,<3.16"
# dependencies = ["pyte==0.8.2", "pillow==13.0.0"]
# ///
"""Capture a real command's terminal screen as light and dark PNGs."""

import argparse
import errno
import fcntl
import os
import pty
import re
import select
import signal
import struct
import tempfile
import termios
import time
from pathlib import Path

import pyte
from PIL import Image, ImageDraw, ImageFont

THEMES = {"dark": ("#141414", "#f3efea"), "light": ("#ffffff", "#2a2522")}
PALETTE = {
    "black": "#000000",
    "red": "#cd3131",
    "green": "#0dbc79",
    "brown": "#e5e510",
    "blue": "#2472c8",
    "magenta": "#bc3fbc",
    "cyan": "#11a8cd",
    "white": "#e5e5e5",
    "brightblack": "#666666",
    "brightred": "#f14c4c",
    "brightgreen": "#23d18b",
    "brightbrown": "#f5f543",
    "brightblue": "#3b8eea",
    "brightmagenta": "#d670d6",
    "brightcyan": "#29b8db",
    "brightwhite": "#ffffff",
}
# pyte misinterprets xterm's private modifyOtherKeys sequence as underline SGR.
KEY_MODE = re.compile(rb"\x1b\[>[0-9;]*m")


def frame(raw, cols, rows):
    screen = pyte.Screen(cols, rows)
    pyte.ByteStream(screen).feed(KEY_MODE.sub(b"", raw))
    return screen


def reply(fd, raw, sent, theme):
    bg = "1414/1414/1414" if theme == "dark" else "ffff/ffff/ffff"
    replies = {
        b"\x1b]11;?": f"\x1b]11;rgb:{bg}\x1b\\".encode(),
        b"\x1b[c": b"\x1b[?1;2c",
        b"\x1b[6n": b"\x1b[1;1R",
    }
    for query, response in replies.items():
        count = raw.count(query)
        for _ in range(sent.get(query, 0), count):
            try:
                os.write(fd, response)
            except OSError as exc:
                if exc.errno != errno.EIO:
                    raise  # The child may have exited after issuing the query.
        sent[query] = count


def exited(pid):
    # Leave the leader unreaped until its process group is stopped, to prevent PID reuse.
    return os.waitid(os.P_PID, pid, os.WEXITED | os.WNOHANG | os.WNOWAIT) is not None


def signal_group(pid, sig):
    try:
        os.killpg(pid, sig)
    except ProcessLookupError:
        pass
    except PermissionError:
        # Darwin returns EPERM for a group containing only an unreaped zombie.
        if not exited(pid):
            raise


def stop_child(pid):
    # pty.fork creates a new session so this signals only the captured command's group.
    if not exited(pid):
        signal_group(pid, signal.SIGTERM)
        deadline = time.monotonic() + 1
        while not exited(pid) and time.monotonic() < deadline:
            time.sleep(0.02)
    # A child of the leader can survive TERM even when the leader has exited.
    signal_group(pid, signal.SIGKILL)
    return os.waitpid(pid, 0)[1]


def capture(args, theme, scratch):
    env = dict(
        os.environ,
        TERM="xterm-256color",
        COLORTERM="truecolor",
        TMPDIR=scratch,
        COLORFGBG="15;0" if theme == "dark" else "0;15",
    )
    env.pop("NO_COLOR", None)
    pid, fd = pty.fork()
    if pid == 0:
        try:
            fcntl.ioctl(
                1, termios.TIOCSWINSZ, struct.pack("HHHH", args.rows, args.cols, 0, 0)
            )
            os.execvpe(args.command[0], args.command, env)
        except OSError as exc:
            os.write(2, f"{exc}\n".encode())
            os._exit(127)

    raw = bytearray()
    sent = {}
    reaped = False
    deadline = time.monotonic() + args.timeout
    try:
        while time.monotonic() < deadline:
            if not select.select([fd], [], [], 0.1)[0]:
                continue
            try:
                data = os.read(fd, 65536)
            except OSError as exc:
                if exc.errno != errno.EIO:
                    raise
                data = b""  # PTY EOF on macOS and Linux.
            if not data:
                if args.until:
                    raise RuntimeError(f"command exited before showing {args.until!r}")
                while time.monotonic() < deadline:
                    if exited(pid):
                        status = stop_child(pid)
                        reaped = True
                        code = os.waitstatus_to_exitcode(status)
                        if code != args.expect_exit:
                            raise RuntimeError(
                                f"command exited {code}, expected {args.expect_exit}"
                            )
                        return frame(raw, args.cols, args.rows)
                    time.sleep(0.02)
                break
            raw += data
            reply(fd, raw, sent, theme)
            if args.until:
                screen = frame(raw, args.cols, args.rows)
                if args.until in "\n".join(screen.display):
                    return screen
        raise RuntimeError(
            f"timed out after {args.timeout}s waiting for {args.until or 'command exit'}"
        )
    finally:
        if not reaped:
            stop_child(pid)
        os.close(fd)


def fonts():
    menlo = Path("/System/Library/Fonts/Menlo.ttc")
    if menlo.exists():
        return (
            ImageFont.truetype(str(menlo), 19, index=0),
            ImageFont.truetype(str(menlo), 19, index=1),
            ImageFont.truetype("/System/Library/Fonts/Apple Symbols.ttf", 19),
        )
    dejavu = Path("/usr/share/fonts/truetype/dejavu")
    normal = ImageFont.truetype(str(dejavu / "DejaVuSansMono.ttf"), 19)
    bold = ImageFont.truetype(str(dejavu / "DejaVuSansMono-Bold.ttf"), 19)
    return normal, bold, normal


def color(value, default):
    if value == "default":
        return default
    if re.fullmatch(r"[0-9a-f]{6}", value):
        return "#" + value
    return PALETTE.get(value, default)


def render(screen, theme, out):
    bg, fg = THEMES[theme]
    normal, bold, symbols = fonts()
    cell_w, cell_h, pad = round(normal.getlength("M")), 28, 24
    image = Image.new(
        "RGB", (screen.columns * cell_w + pad * 2, screen.lines * cell_h + pad * 2), bg
    )
    draw = ImageDraw.Draw(image)
    for y in range(screen.lines):
        for x in range(screen.columns):
            ch = screen.buffer[y][x]
            ink, paper = color(ch.fg, fg), color(ch.bg, bg)
            if ch.reverse:
                ink, paper = paper, ink
            left, top = pad + x * cell_w, pad + y * cell_h
            if paper != bg:
                draw.rectangle(
                    (left, top, left + cell_w - 1, top + cell_h - 1), fill=paper
                )
            if ch.data and ch.data != " ":
                font = (
                    symbols
                    if 0x2800 <= ord(ch.data[0]) <= 0x28FF
                    else (bold if ch.bold else normal)
                )
                draw.text((left, top + 2), ch.data, fill=ink, font=font)
                if ch.underscore:
                    draw.line(
                        (left, top + cell_h - 4, left + cell_w - 1, top + cell_h - 4),
                        fill=ink,
                    )
    image.save(out)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--out", type=Path, required=True, help="directory for the PNGs"
    )
    parser.add_argument(
        "--name", required=True, help="filename prefix, e.g. login-after"
    )
    parser.add_argument("--cols", type=int, default=88)
    parser.add_argument("--rows", type=int, default=16)
    parser.add_argument("--timeout", type=float, default=15)
    parser.add_argument("--theme", choices=["both", *THEMES], default="both")
    parser.add_argument(
        "--until",
        help="capture once this literal string appears on screen, then stop the command",
    )
    parser.add_argument(
        "--expect-exit",
        type=int,
        default=0,
        help="expected exit code when --until is not used",
    )
    parser.add_argument(
        "command", nargs=argparse.REMAINDER, help="command and arguments after --"
    )
    args = parser.parse_args()
    if args.command[:1] == ["--"]:
        args.command = args.command[1:]
    if not args.command or min(args.cols, args.rows, args.timeout) <= 0:
        parser.error("a command and positive dimensions and timeout are required")
    if not re.fullmatch(r"[a-zA-Z0-9_-]+", args.name):
        parser.error(
            "--name must be a filename prefix (letters, digits, hyphens, underscores)"
        )
    args.out.mkdir(parents=True, exist_ok=True)
    themes = THEMES if args.theme == "both" else [args.theme]
    outputs = {theme: args.out / f"{args.name}-{theme}.png" for theme in themes}
    for out in outputs.values():
        out.unlink(missing_ok=True)  # A failed retry must not leave stale screenshots.
    for theme, out in outputs.items():
        with tempfile.TemporaryDirectory(prefix="loco-terminal-") as scratch:
            screen = capture(args, theme, scratch)
        render(screen, theme, out)
        print(out)


if __name__ == "__main__":
    main()
