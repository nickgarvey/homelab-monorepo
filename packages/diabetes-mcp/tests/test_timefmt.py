"""Zone choice and formatting: travel, DST, and the fallbacks."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from zoneinfo import ZoneInfo

import pytest

from diabetes_mcp import timefmt

HOME = ZoneInfo("America/Los_Angeles")
NOW = datetime(2026, 11, 24, 18, 30, tzinfo=UTC)


def test_argument_wins():
    z = timefmt.resolve("Asia/Tokyo", -28800, "ETC/GMT+8", NOW, HOME)
    assert (z.name, z.source) == ("Asia/Tokyo", "argument")


def test_bad_argument():
    with pytest.raises(ValueError, match="IANA"):
        timefmt.resolve("Mars/Olympus", None, None, NOW, HOME)


def test_pump_offset_matching_home_uses_home_zone():
    z = timefmt.resolve(None, -28800, "ETC/GMT+8", NOW, HOME)
    assert (z.name, z.source) == ("America/Los_Angeles", "pump")
    assert "synced in Loop" in z.describe(NOW)["note"]


def test_pump_offset_abroad_is_fixed():
    z = timefmt.resolve(None, 9 * 3600, "ETC/GMT+8", NOW, HOME)
    assert (z.name, z.source) == ("UTC+09:00", "pump")
    assert timefmt.when(NOW, NOW, z)["local"] == "Wed Nov 25 3:30 AM UTC+09:00"


def test_profile_then_home():
    assert timefmt.resolve(None, None, "ETC/GMT-1", NOW, HOME).name == "UTC+01:00"
    assert timefmt.resolve(None, None, "Europe/Paris", NOW, HOME).name == "Europe/Paris"
    z = timefmt.resolve(None, None, None, NOW, HOME)
    assert (z.name, z.source) == ("America/Los_Angeles", "home")


def test_when_three_forms():
    z = timefmt.resolve(None, None, None, NOW, HOME)
    t = NOW + timedelta(hours=58, minutes=12)
    assert timefmt.when(t, NOW, z) == {"utc": "2026-11-27T04:42:00Z", "local": "Thu Nov 26 8:42 PM PST",
                                       "relative": "in 2 d 10 h"}
    assert timefmt.when(NOW - timedelta(minutes=200), NOW, z)["relative"] == "3 h 20 m ago"
    assert timefmt.relative(timedelta(seconds=20)) == "now"


def test_parse():
    assert timefmt.parse("2026-11-24T07:25:25.000Z") == datetime(2026, 11, 24, 7, 25, 25, tzinfo=UTC)
    assert timefmt.parse(1795543795000) == datetime(2026, 11, 24, 18, 9, 55, tzinfo=UTC)
    assert timefmt.parse("1795543795000") == timefmt.parse(1795543795000)
    assert timefmt.parse("garbage") is None and timefmt.parse(None) is None
