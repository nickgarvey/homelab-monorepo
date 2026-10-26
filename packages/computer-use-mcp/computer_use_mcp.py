"""MCP server that drives an X11 desktop with xdotool and scrot.

The tools mirror the members of Anthropic's computer_toolset_20260801, so a
model trained on that toolset finds the same names and arguments here.
Coordinates are screen pixels: screenshots are taken at screen size and never
scaled, so there is no coordinate mapping to get wrong.
"""

import functools
import io
import os
import subprocess
import tempfile
import threading
import time
from typing import Literal

from mcp.server.mcpserver import Image, MCPServer
from PIL import Image as PILImage

# Time for the screen to settle after an action before the screenshot that
# answers it.
SETTLE_SECONDS = float(os.environ.get("COMPUTER_USE_SETTLE", "0.5"))
TYPE_CHUNK = 50
TYPE_DELAY_MS = 12

# The server may run calls concurrently; desktop actions must not interleave.
LOCK = threading.Lock()

server = MCPServer(
    "computer",
    instructions=(
        "Controls the X11 desktop (XFCE, 1280x800) of the browser VM. "
        "Coordinates are screen pixels and match the screenshots exactly. "
        "Actions return a screenshot taken after the screen settles. "
        "For web pages prefer the playwright server; use this for anything "
        "outside the page: native dialogs, other apps, the desktop."
    ),
)


def tool(fn):
    """Register fn as a tool that runs alone."""

    @functools.wraps(fn)
    def run(*args, **kwargs):
        with LOCK:
            return fn(*args, **kwargs)

    return server.tool()(run)


def xdotool(*args: str) -> str:
    return subprocess.run(
        ["xdotool", *args], check=True, capture_output=True, text=True
    ).stdout


def grab() -> PILImage.Image:
    with tempfile.TemporaryDirectory() as d:
        path = os.path.join(d, "s.png")
        subprocess.run(["scrot", "--overwrite", "--pointer", path], check=True)
        img = PILImage.open(path)
        img.load()
        return img


def png(img: PILImage.Image) -> Image:
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return Image(data=buf.getvalue(), format="png")


def settled() -> Image:
    time.sleep(SETTLE_SECONDS)
    return png(grab())


def modifiers(text: str | None) -> list[str]:
    return [m for m in (text or "").split("+") if m]


def with_modifiers(text: str | None, *args: str) -> None:
    mods = modifiers(text)
    for m in mods:
        xdotool("keydown", m)
    try:
        xdotool(*args)
    finally:
        for m in reversed(mods):
            xdotool("keyup", m)


def move(coordinate: list[int] | None) -> None:
    if coordinate is not None:
        x, y = coordinate
        xdotool("mousemove", "--sync", str(x), str(y))


def click(coordinate: list[int] | None, text: str | None, button: int, repeat: int = 1) -> Image:
    move(coordinate)
    with_modifiers(text, "click", "--repeat", str(repeat), "--delay", "80", str(button))
    return settled()


@tool
def screenshot() -> Image:
    """Take a screenshot of the whole screen."""
    return png(grab())


@tool
def zoom(region: list[int]) -> Image:
    """Return [x0, y0, x1, y1] of the screen enlarged to screenshot size.

    Coordinates for later actions stay in full-screenshot space.
    """
    x0, y0, x1, y1 = region
    img = grab()
    crop = img.crop((x0, y0, x1, y1))
    scale = min(img.width / crop.width, img.height / crop.height)
    size = (max(1, round(crop.width * scale)), max(1, round(crop.height * scale)))
    return png(crop.resize(size, PILImage.Resampling.LANCZOS))


@tool
def left_click(coordinate: list[int] | None = None, text: str | None = None) -> Image:
    """Left-click at [x, y] (or at the cursor). text holds modifiers, e.g. "ctrl+shift"."""
    return click(coordinate, text, 1)


@tool
def right_click(coordinate: list[int] | None = None, text: str | None = None) -> Image:
    """Right-click at [x, y] (or at the cursor). text holds modifiers."""
    return click(coordinate, text, 3)


@tool
def middle_click(coordinate: list[int] | None = None, text: str | None = None) -> Image:
    """Middle-click at [x, y] (or at the cursor). text holds modifiers."""
    return click(coordinate, text, 2)


@tool
def double_click(coordinate: list[int] | None = None, text: str | None = None) -> Image:
    """Double-click at [x, y] (or at the cursor). text holds modifiers."""
    return click(coordinate, text, 1, repeat=2)


@tool
def triple_click(coordinate: list[int] | None = None, text: str | None = None) -> Image:
    """Triple-click at [x, y] (or at the cursor). text holds modifiers."""
    return click(coordinate, text, 1, repeat=3)


@tool
def left_click_drag(start_coordinate: list[int], coordinate: list[int], text: str | None = None) -> Image:
    """Press the left button at start_coordinate, drag to coordinate, release."""
    mods = modifiers(text)
    for m in mods:
        xdotool("keydown", m)
    try:
        move(start_coordinate)
        xdotool("mousedown", "1")
        move(coordinate)
        xdotool("mouseup", "1")
    finally:
        for m in reversed(mods):
            xdotool("keyup", m)
    return settled()


@tool
def mouse_move(coordinate: list[int]) -> Image:
    """Move the pointer to [x, y], e.g. to hover."""
    move(coordinate)
    return settled()


@tool
def left_mouse_down() -> str:
    """Press the left button at the cursor. Pair with mouse_move and left_mouse_up."""
    xdotool("mousedown", "1")
    return "ok"


@tool
def left_mouse_up() -> Image:
    """Release the left button at the cursor."""
    xdotool("mouseup", "1")
    return settled()


@tool
def cursor_position() -> str:
    """Return the pointer position as X=…, Y=…."""
    loc = dict(
        line.split("=", 1) for line in xdotool("getmouselocation", "--shell").splitlines() if "=" in line
    )
    return f"X={loc['X']}, Y={loc['Y']}"


@tool
def scroll(
    scroll_direction: Literal["up", "down", "left", "right"],
    scroll_amount: int,
    coordinate: list[int] | None = None,
    text: str | None = None,
) -> Image:
    """Scroll scroll_amount wheel clicks at [x, y] (or at the cursor). text holds modifiers."""
    button = {"up": 4, "down": 5, "left": 6, "right": 7}[scroll_direction]
    move(coordinate)
    with_modifiers(text, "click", "--repeat", str(scroll_amount), "--delay", "30", str(button))
    return settled()


@tool
def type(text: str) -> Image:
    """Type text at the current focus."""
    for i in range(0, len(text), TYPE_CHUNK):
        xdotool("type", "--delay", str(TYPE_DELAY_MS), "--", text[i : i + TYPE_CHUNK])
    return settled()


@tool
def key(text: str, repeat: int = 1) -> Image:
    """Press a key or chord as xdotool keysyms, e.g. "Return", "ctrl+s", "alt+Tab"."""
    xdotool("key", "--repeat", str(max(1, min(repeat, 100))), "--delay", "50", "--", text)
    return settled()


@tool
def hold_key(text: str, duration: float) -> Image:
    """Hold a key or chord down for duration seconds (up to 300)."""
    xdotool("keydown", "--", text)
    try:
        time.sleep(max(0.0, min(duration, 300.0)))
    finally:
        xdotool("keyup", "--", text)
    return settled()


@tool
def wait(duration: float) -> Image:
    """Wait duration seconds (up to 300), then take a screenshot."""
    time.sleep(max(0.0, min(duration, 300.0)))
    return png(grab())


def main() -> None:
    server.run("stdio")


if __name__ == "__main__":
    main()
