"""The tools against the fake Nightscout, with the clock fixed."""

from __future__ import annotations

import asyncio
from datetime import UTC, datetime, timedelta

import pytest

from conftest import NOW, TOKEN
from diabetes_mcp import stats, tools
from diabetes_mcp.nightscout import NightscoutError


def run(coro):
    return asyncio.run(coro)


def test_pod_expiry(ns, cfg):
    out = run(tools.device_lifetimes(ns, NOW, cfg))
    pod = out["pod"]
    # Changed 2026-11-24T07:25:25Z (Nov 23 11:25 PM PST): 72 h and 80 h later.
    assert pod["expires"]["utc"] == "2026-11-27T07:25:25Z"
    assert pod["expires"]["local"] == "Thu Nov 26 11:25 PM PST"
    assert pod["grace_ends"]["utc"] == "2026-11-27T15:25:25Z"
    assert pod["status"] == "active" and pod["model"] == "Insulet Omnipod 5"
    assert out["time_zone"]["source"] == "pump"
    cgm = out["cgm"]
    assert cgm["expires"]["utc"] == "2026-11-29T05:42:06Z" and cgm["device"] == "Dexcom G7"
    assert out["loop"] == {"version": "3.14.11.4"}


def test_travel_moves_local_time_not_the_instant(ns, cfg):
    home = run(tools.device_lifetimes(ns, NOW, cfg))["pod"]["expires"]
    tokyo = run(tools.device_lifetimes(ns, NOW, cfg, timezone="Asia/Tokyo"))
    assert tokyo["pod"]["expires"]["utc"] == home["utc"]
    assert tokyo["pod"]["expires"]["local"] == "Fri Nov 27 4:25 PM JST"
    assert tokyo["pod"]["expires"]["relative"] == home["relative"]
    # The pump is still on Pacific time: say so.
    assert "UTC-08:00" in tokyo["warnings"][0]


def test_pump_synced_abroad(ns, fake, cfg):
    fake.data["devicestatus"][0]["pump"]["secondsFromGMT"] = 9 * 3600
    out = run(tools.device_lifetimes(ns, NOW, cfg))
    assert out["time_zone"]["name"] == "UTC+09:00"
    assert out["pod"]["expires"]["local"] == "Fri Nov 27 4:25 PM UTC+09:00"
    assert "warnings" not in out


def test_expiry_across_dst(ns, fake, cfg):
    # Changed Sat Oct 31 10:00 PM PDT; DST ends Nov 1. 72 real hours later is 9:00 PM PST.
    site = next(t for t in fake.data["treatments"] if t["eventType"] == "Site Change")
    site["created_at"] = "2026-11-01T05:00:00.000Z"
    now = datetime(2026, 11, 2, tzinfo=UTC)
    pod = run(tools.device_lifetimes(ns, now, cfg))["pod"]
    assert pod["started"]["local"] == "Sat Oct 31 10:00 PM PDT"
    assert pod["expires"]["utc"] == "2026-11-04T05:00:00Z"
    assert pod["expires"]["local"] == "Tue Nov 3 9:00 PM PST"


def test_expired_and_grace(ns, cfg):
    assert run(tools.device_lifetimes(ns, NOW + timedelta(hours=65), cfg))["pod"]["status"] == \
        "expired, in grace period"
    assert run(tools.device_lifetimes(ns, NOW + timedelta(hours=70), cfg))["pod"]["status"] == "past grace period"


def test_no_sensor_start(ns, fake, cfg):
    fake.data["treatments"] = [t for t in fake.data["treatments"] if t["eventType"] != "Sensor Start"]
    assert run(tools.device_lifetimes(ns, NOW, cfg))["cgm"] == {"note": "Nightscout has no Sensor Start event."}


def test_current_status(ns, cfg):
    out = run(tools.current_status(ns, NOW, cfg))
    g = out["glucose"]
    assert g["mg_dl"] == 150 and g["trend"] == "↘ falling slowly" and g["time"]["relative"] == "20 m ago"
    loop = out["loop"]
    assert loop["iob_units"] == 1.25 and loop["cob_grams"] == 10
    assert loop["forecast"]["min_mg_dl"] == 117 and loop["forecast"]["eventual_mg_dl"] == 125
    assert out["pump"]["reservoir_note"].startswith("Not reported")
    assert out["phone"] == {"battery_percent": 80, "charging": False}
    assert out["override"] == {"active": False}
    # 20 minutes old: stale.
    assert any("glucose reading is 20 m ago" in w for w in out["warnings"])
    fresh = run(tools.current_status(ns, datetime(2026, 11, 24, 18, 12, tzinfo=UTC), cfg))
    assert "warnings" not in fresh


def test_reservoir_and_override(ns, fake, cfg):
    ds = fake.data["devicestatus"][0]
    ds["pump"]["reservoir"] = 32.5
    ds["override"] = {"active": True, "name": "Exercise", "multiplier": 0.5, "duration": 3600,
                      "timestamp": "2026-11-24T18:00:00Z", "currentCorrectionRange": {"minValue": 140, "maxValue": 160}}
    out = run(tools.current_status(ns, NOW, cfg))
    assert out["pump"]["reservoir_units"] == 32.5 and "reservoir_note" not in out["pump"]
    assert out["override"]["insulin_needs_percent"] == 50
    assert out["override"]["target_mg_dl"] == [140, 160]
    assert out["override"]["ends"]["local"] == "Tue Nov 24 11:00 AM PST"


def test_treatments(ns, fake, cfg):
    out = run(tools.treatments(ns, NOW, cfg, hours=24))
    events = [t["event"] for t in out["treatments"]]
    assert events == ["Site Change", "Carb Correction", "Correction Bolus"]
    assert out["daily_totals"] == {"Mon Nov 23": {"bolus_units": 0.0, "carbs_grams": 0.0},
                                   "Tue Nov 24": {"bolus_units": 0.2, "carbs_grams": 30.0}}
    # Temp basals were excluded on the server side.
    assert any(r.url.params.get("find[eventType][$ne]") == "Temp Basal" for r in fake.requests)
    basals = run(tools.treatments(ns, NOW, cfg, hours=24, event_type="Temp Basal"))["treatments"]
    assert basals[0]["rate_units_per_hour"] == 0 and basals[0]["duration_minutes"] == 4.7


def test_glucose_history(ns, cfg):
    out = run(tools.glucose_history(ns, NOW, cfg, hours=1))
    assert out["summary"]["readings"] == 2 and out["summary"]["mean_mg_dl"] == 153.0
    assert out["readings"] == [{"local": "Tue 24 10:04", "mg_dl": 156}, {"local": "Tue 24 10:09", "mg_dl": 150}]
    with pytest.raises(ValueError):
        run(tools.glucose_history(ns, NOW, cfg, hours=1000))


def test_stats():
    s = stats.glucose_stats([50, 65, 100, 150, 200, 260], span_minutes=30)
    assert s["time_below_54_percent"] == 16.7 and s["time_below_70_percent"] == 33.3
    assert s["time_in_range_70_180_percent"] == 33.3 and s["time_above_250_percent"] == 16.7
    assert s["gmi_percent"] == round(3.31 + 0.02392 * s["mean_mg_dl"], 1)
    assert s["coverage_percent"] == 100.0
    assert stats.downsample(list(range(1000)), 5) == [0, 250, 500, 749, 999]


def test_therapy_settings_hides_device_token(ns, cfg):
    out = run(tools.therapy_settings(ns, NOW, cfg))
    assert out["basal_units_per_hour"] == [{"start": "00:00", "value": 0.8}, {"start": "06:00", "value": 1.0}]
    assert out["schedule_time_zone"] == {"profile": "ETC/GMT+8", "utc_offset": "UTC-08:00"}
    assert out["loop"]["maximumBolus"] == 10
    assert out["override_presets"][0] == {"name": "Exercise", "target_mg_dl": [140, 160],
                                          "insulin_needs_percent": 50, "duration_minutes": 60}
    assert "FAKE-APNS-DEVICE-TOKEN" not in repr(out) and "bundleIdentifier" not in repr(out)


def test_errors_do_not_leak_the_token(ns, fake, cfg):
    fake.status = 500
    with pytest.raises(NightscoutError) as e:
        run(tools.device_lifetimes(ns, NOW, cfg))
    assert TOKEN not in str(e.value) and "500" in str(e.value)
