"""Touch UI for skyforge's BTT HDMI5: Load, Unload and Stop buttons over moonraker, klipper and kernel logs.

Draws with pygame into an offscreen surface and copies it to /dev/fb0
(800x480, RGB565). Touches come from the panel's evdev device. Printer
commands go to Moonraker on loopback, which trusts ::1.
"""

import collections
import fcntl
import json
import os
import re
import signal
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

os.environ["SDL_VIDEODRIVER"] = "dummy"
os.environ["PYGAME_HIDE_SUPPORT_PROMPT"] = "1"

import evdev  # noqa: E402
import pygame  # noqa: E402

W, H = 800, 480
FB = "/dev/fb0"
TOUCH_NAME = "BIQU BTT-HDMI5"
TOUCH_MAX = 4096
MOONRAKER = "http://[::1]:7125"
FONT_SANS = os.environ["SCREEN_FONT_SANS"]
FONT_BOLD = os.environ["SCREEN_FONT_BOLD"]
FONT_MONO = os.environ["SCREEN_FONT_MONO"]
MATERIALS = {"PLA": 220, "ABS": 250}
# Moonraker's "[file.py:func()] - " and klippy's "INFO:root:" cost a third of a pane's width.
LOG_PREFIX = re.compile(r"^(\[[^\]]*\] - |[A-Z]+:root:)")

KDSETMODE = 0x4B3A
KD_TEXT, KD_GRAPHICS = 0, 1

BG = (16, 18, 22)
PANEL = (28, 31, 38)
TEXT = (226, 230, 236)
DIM = (130, 138, 150)
ERR = (255, 120, 110)
BLUE = (40, 110, 200)
AMBER = (196, 130, 20)
RED = (200, 40, 40)
GREEN = (60, 160, 90)
GREY = (60, 64, 72)

STATUS_H = 36
BUTTONS = pygame.Rect(8, 44, 784, 188)
LOGS_Y = 240


class State:
    """Everything the UI draws, shared between the worker threads and the draw loop."""

    def __init__(self):
        self.lock = threading.Lock()
        self.dirty = threading.Event()
        self.status = None  # last printer/objects/query result, or None
        self.status_error = "Connecting to Moonraker"
        self.busy = None  # "Loading PLA" while a macro runs
        self.flash = None  # (text, colour, expires_at)
        self.choosing = None  # "load" / "unload" while the material row shows
        self.choosing_until = 0.0
        self.pressed = None  # (button id, until)
        self.logs = {}

    def set(self, **kw):
        with self.lock:
            for k, v in kw.items():
                setattr(self, k, v)
        self.dirty.set()


S = State()


def api(method, path, timeout=5, **params):
    url = MOONRAKER + path
    if params:
        url += "?" + urllib.parse.urlencode(params)
    req = urllib.request.Request(url, method=method, data=b"" if method == "POST" else None)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.load(r)["result"]
    except urllib.error.HTTPError as e:
        try:
            msg = json.load(e)["error"]["message"]
        except Exception:
            msg = str(e)
        raise RuntimeError(msg) from None


def poll_status():
    query = "/printer/objects/query?webhooks&print_stats&extruder&display_status"
    while True:
        try:
            st = api("GET", query)["status"]
            S.set(status=st, status_error=None)
        except Exception as e:
            S.set(status=None, status_error=f"Moonraker: {e}")
        time.sleep(1)


def run_macro(label, script):
    def work():
        S.set(busy=label)
        try:
            api("POST", "/printer/gcode/script", timeout=900, script=script)
            S.set(flash=(f"{label}: done", GREEN, time.time() + 8))
        except Exception as e:
            S.set(flash=(str(e), ERR, time.time() + 15))
        finally:
            S.set(busy=None)

    threading.Thread(target=work, daemon=True).start()


def run_call(label, path):
    def work():
        try:
            api("POST", path)
            S.set(flash=(label, AMBER, time.time() + 8))
        except Exception as e:
            S.set(flash=(f"{label} failed: {e}", ERR, time.time() + 15))

    threading.Thread(target=work, daemon=True).start()


def follow_journal(name, args):
    S.logs[name] = collections.deque(maxlen=200)
    while True:
        p = subprocess.Popen(
            ["journalctl", "--follow", "--lines=40", "--output=cat", *args],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            errors="replace",
        )
        for line in p.stdout:
            with S.lock:
                S.logs[name].append(LOG_PREFIX.sub("", line.rstrip("\n")).expandtabs(4))
            S.dirty.set()
        p.wait()
        time.sleep(5)


def klippy_state(st):
    """Return (state, message) for the status bar."""
    if st is None:
        return "offline", S.status_error or ""
    wh = st.get("webhooks", {})
    state = wh.get("state", "unknown")
    if state != "ready":
        lines = (wh.get("state_message") or "").splitlines()
        return state, lines[0] if lines else ""
    ps = st.get("print_stats", {}).get("state", "")
    msg = st.get("display_status", {}).get("message") or ""
    return ps or state, msg


def klippy_halted(st):
    return st is not None and st.get("webhooks", {}).get("state") in ("shutdown", "error")


def printing(st):
    return st is not None and st.get("print_stats", {}).get("state") == "printing"


# Button layout. The Stop slot never moves, so it is always where a thumb expects it.
SLOT_W = 252
GAP = (BUTTONS.w - 3 * SLOT_W) // 2
SLOTS = [pygame.Rect(BUTTONS.x + i * (SLOT_W + GAP), BUTTONS.y, SLOT_W, BUTTONS.h) for i in range(3)]
CHOICE_W = (SLOTS[1].right - SLOTS[0].x - 2 * GAP - 96) // 2
CHOICES = {
    "PLA": pygame.Rect(SLOTS[0].x, BUTTONS.y, CHOICE_W, BUTTONS.h),
    "ABS": pygame.Rect(SLOTS[0].x + CHOICE_W + GAP, BUTTONS.y, CHOICE_W, BUTTONS.h),
    "cancel": pygame.Rect(SLOTS[1].right - 96, BUTTONS.y, 96, BUTTONS.h),
}


def layout():
    """Return [(id, rect, label, sublabel, colour, enabled)] for the current state."""
    with S.lock:
        st, busy, choosing = S.status, S.busy, S.choosing
    ready = st is not None and st.get("webhooks", {}).get("state") == "ready"
    can_move = ready and busy is None and not printing(st)
    out = []
    if choosing:
        verb = "Load" if choosing == "load" else "Unload"
        for m, t in MATERIALS.items():
            out.append((m, CHOICES[m], m, f"{verb} at {t}°C", BLUE if choosing == "load" else AMBER, True))
        out.append(("cancel", CHOICES["cancel"], "Back", "", GREY, True))
    else:
        why = "busy" if busy else "printing" if printing(st) else "" if ready else "not ready"
        out.append(("load", SLOTS[0], "LOAD", why or "filament", BLUE, can_move))
        out.append(("unload", SLOTS[1], "UNLOAD", why or "filament", AMBER, can_move))
    if klippy_halted(st):
        out.append(("restart", SLOTS[2], "RESTART", "firmware", GREEN, True))
    else:
        out.append(("stop", SLOTS[2], "STOP", "emergency", RED, st is not None))
    return out


def on_tap(x, y):
    now = time.time()
    for bid, rect, _, _, _, enabled in layout():
        if not rect.collidepoint(x, y) or not enabled:
            continue
        S.set(pressed=(bid, now + 0.25))
        if bid == "stop":
            S.set(choosing=None)
            run_call("Emergency stop sent", "/printer/emergency_stop")
        elif bid == "restart":
            run_call("Firmware restart sent", "/printer/firmware_restart")
        elif bid in ("load", "unload"):
            S.set(choosing=bid, choosing_until=now + 15)
        elif bid == "cancel":
            S.set(choosing=None)
        elif bid in MATERIALS:
            verb = S.choosing
            S.set(choosing=None)
            macro = "LOAD_FILAMENT" if verb == "load" else "UNLOAD_FILAMENT"
            label = ("Loading " if verb == "load" else "Unloading ") + bid
            run_macro(label, f"{macro} MATERIAL={bid}")
        return


def read_touch():
    while True:
        dev = next((evdev.InputDevice(p) for p in evdev.list_devices() if evdev.InputDevice(p).name == TOUCH_NAME), None)
        if dev is None:
            time.sleep(5)
            continue
        x = y = 0
        down = False
        last = 0.0
        try:
            for ev in dev.read_loop():
                if ev.type == evdev.ecodes.EV_ABS and ev.code == evdev.ecodes.ABS_X:
                    x = ev.value * W // TOUCH_MAX
                elif ev.type == evdev.ecodes.EV_ABS and ev.code == evdev.ecodes.ABS_Y:
                    y = ev.value * H // TOUCH_MAX
                elif ev.type == evdev.ecodes.EV_KEY and ev.code == evdev.ecodes.BTN_TOUCH:
                    down = ev.value == 1
                elif ev.type == evdev.ecodes.EV_SYN and down:
                    down = False
                    if time.time() - last > 0.3:
                        last = time.time()
                        on_tap(x, y)
        except OSError:
            time.sleep(2)


class Painter:
    def __init__(self):
        pygame.font.init()
        self.surf = pygame.Surface((W, H), 0, 16)
        self.big = pygame.font.Font(FONT_BOLD, 40)
        self.mid = pygame.font.Font(FONT_BOLD, 26)
        self.small = pygame.font.Font(FONT_SANS, 16)
        self.status_font = pygame.font.Font(FONT_SANS, 17)
        self.mono = pygame.font.Font(FONT_MONO, 10)
        self.title = pygame.font.Font(FONT_BOLD, 11)
        self.mono_w, self.mono_h = self.mono.size("M")

    def text(self, font, s, colour, **pos):
        img = font.render(s, True, colour)
        self.surf.blit(img, img.get_rect(**pos))
        return img.get_width()

    def clip_text(self, font, s, colour, width, **pos):
        while s and font.size(s)[0] > width:
            s = s[:-2] + "…"
            if len(s) <= 1:
                break
        return self.text(font, s, colour, **pos)

    def draw(self):
        s = self.surf
        s.fill(BG)
        with S.lock:
            st, busy, flash, pressed = S.status, S.busy, S.flash, S.pressed
            logs = {k: list(v) for k, v in S.logs.items()}
        now = time.time()

        # Status bar: state and message on the left, extruder temperature on the right.
        pygame.draw.rect(s, PANEL, (0, 0, W, STATUS_H))
        state, msg = klippy_state(st)
        colour = ERR if state in ("offline", "shutdown", "error") else GREEN if state == "printing" else TEXT
        right = W - 10
        if st is not None and "extruder" in st:
            e = st["extruder"]
            temp = f"Nozzle {e.get('temperature', 0):.0f}°"
            if e.get("target"):
                temp += f" → {e['target']:.0f}°"
            right -= self.text(self.status_font, temp, TEXT, midright=(right, STATUS_H // 2)) + 16
        x = 10 + self.text(self.status_font, state.upper(), colour, midleft=(10, STATUS_H // 2)) + 14
        if flash and flash[2] > now:
            line, lcolour = flash[0], flash[1]
        elif busy:
            line, lcolour = f"{busy}…  {msg}", AMBER
        else:
            line, lcolour = msg, DIM
        self.clip_text(self.status_font, line, lcolour, right - x, midleft=(x, STATUS_H // 2))

        # Buttons.
        for bid, rect, label, sub, col, enabled in layout():
            if not enabled:
                col = GREY
            elif pressed and pressed[0] == bid and pressed[1] > now:
                col = tuple(min(255, c + 60) for c in col)
            pygame.draw.rect(s, col, rect, border_radius=14)
            font = self.big if rect.w > 200 else self.mid
            fg = TEXT if enabled else DIM
            cy = rect.centery - (12 if sub else 0)
            self.text(font, label, fg, center=(rect.centerx, cy))
            if sub:
                self.text(self.small, sub, fg, center=(rect.centerx, cy + 36))

        # Logs: three panes, newest line at the bottom, long lines wrapped.
        pane_w = (W - 4 * 4) // 3
        for i, (name, title) in enumerate((("moonraker", "MOONRAKER"), ("klipper", "KLIPPER"), ("kernel", "KERNEL"))):
            r = pygame.Rect(4 + i * (pane_w + 4), LOGS_Y, pane_w, H - LOGS_Y - 4)
            pygame.draw.rect(s, PANEL, r, border_radius=6)
            self.text(self.title, title, DIM, topleft=(r.x + 6, r.y + 3))
            body = pygame.Rect(r.x + 5, r.y + 18, r.w - 10, r.h - 21)
            cols = max(1, body.w // self.mono_w)
            rows = body.h // self.mono_h
            wrapped = []
            for line in logs.get(name, [])[-rows:]:
                wrapped.extend([line[j:j + cols] for j in range(0, len(line), cols)] or [""])
            y = body.bottom - self.mono_h
            for line in reversed(wrapped[-rows:]):
                self.text(self.mono, line, TEXT, topleft=(body.x, y))
                y -= self.mono_h

    def blit_fb(self, fb):
        fb.seek(0)
        fb.write(self.surf.get_buffer().raw)
        fb.flush()


def main():
    tty = sys.stdin.fileno()  # tty1, from TTYPath in screen.nix

    def restore(*_):
        fcntl.ioctl(tty, KDSETMODE, KD_TEXT)
        sys.exit(0)

    signal.signal(signal.SIGTERM, restore)
    signal.signal(signal.SIGINT, restore)
    # Graphics mode stops the console drawing over the UI.
    fcntl.ioctl(tty, KDSETMODE, KD_GRAPHICS)

    for name, args in (
        ("moonraker", ["--unit=moonraker.service"]),
        ("klipper", ["--unit=klipper.service"]),
        ("kernel", ["--dmesg"]),
    ):
        threading.Thread(target=follow_journal, args=(name, args), daemon=True).start()
    threading.Thread(target=poll_status, daemon=True).start()
    threading.Thread(target=read_touch, daemon=True).start()

    painter = Painter()
    with open(FB, "wb", buffering=0) as fb:
        while True:
            S.dirty.wait(timeout=1)
            S.dirty.clear()
            with S.lock:
                if S.choosing and time.time() > S.choosing_until:
                    S.choosing = None
            painter.draw()
            painter.blit_fb(fb)
            time.sleep(0.1)


if __name__ == "__main__":
    main()
