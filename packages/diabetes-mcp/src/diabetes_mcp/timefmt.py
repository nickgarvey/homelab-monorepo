"""Time zones and time formatting, built to keep working while the user travels.

Every calculation is on UTC instants. A time zone is chosen only to show those
instants as local wall-clock times. Loop uploads all of its timestamps in UTC
(Nightscout's `utcOffset` is always 0) and never the phone's time zone, so the
zone comes from, in order:

1. the caller's `timezone` argument (an IANA name);
2. the pump's clock offset, `pump.secondsFromGMT` in the newest devicestatus.
   This is Loop's therapy time zone: it follows the phone only once the pump
   time has been synced in Loop after a time-zone change;
3. the profile's `timezone`, which Loop uploads as a fixed offset ("ETC/GMT+8");
4. the home zone.

An offset from 2 or 3 that matches the home zone's current offset is shown as
the home zone, which knows about DST; any other offset is shown as a fixed
offset ("UTC+09:00").
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta, timezone, tzinfo
from typing import Any
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

PUMP_NOTE = ("Loop's pump time zone. After you change time zones it stays on the old zone until the pump time "
             "is synced in Loop.")


@dataclass(frozen=True)
class Zone:
    tz: tzinfo
    name: str
    source: str  # "argument", "pump", "profile" or "home"

    def describe(self, now: datetime) -> dict[str, Any]:
        out: dict[str, Any] = {"name": self.name, "source": self.source,
                               "utc_offset": offset_label(now.astimezone(self.tz).utcoffset())}
        if self.source == "pump":
            out["note"] = PUMP_NOTE
        return out


def offset_label(delta: timedelta | None) -> str:
    seconds = int((delta or timedelta()).total_seconds())
    sign = "+" if seconds >= 0 else "-"
    h, m = divmod(abs(seconds) // 60, 60)
    return f"UTC{sign}{h:02d}:{m:02d}"


def parse_profile_zone(name: str | None) -> int | ZoneInfo | None:
    """Loop's "ETC/GMT+8" style names give an offset in seconds (POSIX sign, so +8 is UTC-8); IANA names a zone."""
    if not name:
        return None
    m = re.fullmatch(r"(?i)etc/gmt([+-]\d{1,2})", name.strip())
    if m:
        return -int(m.group(1)) * 3600
    try:
        return ZoneInfo(name.strip())
    except (ZoneInfoNotFoundError, ValueError):
        return None


def resolve(argument: str | None, pump_seconds: Any, profile_zone: str | None, now: datetime,
            home: ZoneInfo) -> Zone:
    if argument:
        try:
            return Zone(ZoneInfo(argument), argument, "argument")
        except (ZoneInfoNotFoundError, ValueError) as e:
            raise ValueError(f"unknown time zone {argument!r}: pass an IANA name such as 'Asia/Tokyo'") from e

    def from_offset(seconds: int, source: str) -> Zone:
        if home.utcoffset(now.astimezone(home)) == timedelta(seconds=seconds):
            return Zone(home, home.key, source)
        tz = timezone(timedelta(seconds=seconds))
        return Zone(tz, offset_label(timedelta(seconds=seconds)), source)

    if isinstance(pump_seconds, (int, float)) and not isinstance(pump_seconds, bool):
        return from_offset(int(pump_seconds), "pump")
    profile = parse_profile_zone(profile_zone)
    if isinstance(profile, int):
        return from_offset(profile, "profile")
    if isinstance(profile, ZoneInfo):
        return Zone(profile, profile.key, "profile")
    return Zone(home, home.key, "home")


def parse(value: Any) -> datetime | None:
    """A Nightscout time: ISO 8601 text, or epoch milliseconds."""
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return datetime.fromtimestamp(value / 1000, UTC)
    if isinstance(value, str) and value:
        if value.isdigit():
            return datetime.fromtimestamp(int(value) / 1000, UTC)
        try:
            t = datetime.fromisoformat(value)
        except ValueError:
            return None
        return t if t.tzinfo else t.replace(tzinfo=UTC)
    return None


def relative(delta: timedelta) -> str:
    seconds = int(round(delta.total_seconds()))
    if abs(seconds) < 60:
        return "now"
    minutes = abs(seconds) // 60
    d, rem = divmod(minutes, 1440)
    h, m = divmod(rem, 60)
    parts = [f"{d} d"] if d else []
    if h or d:
        parts.append(f"{h} h")
    if not d:
        parts.append(f"{m} m")
    text = " ".join(parts)
    return f"in {text}" if seconds > 0 else f"{text} ago"


def when(t: datetime, now: datetime, zone: Zone) -> dict[str, str]:
    """One instant in three forms: UTC, local wall-clock time in `zone`, and relative to now."""
    local = t.astimezone(zone.tz)
    return {
        "utc": t.astimezone(UTC).isoformat(timespec="seconds").replace("+00:00", "Z"),
        "local": local.strftime("%a %b %-d %-I:%M %p ") + (local.tzname() or offset_label(local.utcoffset())),
        "relative": relative(t - now),
    }
