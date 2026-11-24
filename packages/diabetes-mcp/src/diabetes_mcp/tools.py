"""The tools' logic: read Nightscout and shape answers. server.py exposes these over MCP.

Every function takes the current time as `now`, so tests can fix it, and a
caller's `timezone`, resolved by timefmt.resolve.
"""

from __future__ import annotations

import asyncio
from collections import defaultdict
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Any
from zoneinfo import ZoneInfo

from . import stats, timefmt
from .nightscout import Nightscout

TRENDS = {
    "DoubleUp": "⇈ rising fast", "SingleUp": "↑ rising", "FortyFiveUp": "↗ rising slowly", "Flat": "→ steady",
    "FortyFiveDown": "↘ falling slowly", "SingleDown": "↓ falling", "DoubleDown": "⇊ falling fast",
    "NOT COMPUTABLE": "no trend", "RATE OUT OF RANGE": "rate out of range",
}
STALE = timedelta(minutes=15)
MAX_HOURS = 14 * 24
# Loop schedules a temp basal every 5 minutes; listing them buries everything else.
TEMP_BASAL = "Temp Basal"
LOOP_SETTINGS = ("dosingEnabled", "dosingStrategy", "maximumBasalRatePerHour", "maximumBolus", "minimumBGGuard",
                 "preMealTargetRange")


@dataclass(frozen=True)
class Config:
    home: ZoneInfo
    pod_hours: float = 72
    pod_grace_hours: float = 8
    cgm_hours: float = 240
    cgm_grace_hours: float = 12


def num(v: Any) -> float | None:
    return v if isinstance(v, (int, float)) and not isinstance(v, bool) else None


def compact(d: dict[str, Any]) -> dict[str, Any]:
    return {k: v for k, v in d.items() if v is not None and v != {}}


def zone_for(timezone: str | None, ds: dict[str, Any] | None, profile: dict[str, Any] | None, now: datetime,
             cfg: Config) -> timefmt.Zone:
    pump_seconds = ((ds or {}).get("pump") or {}).get("secondsFromGMT")
    return timefmt.resolve(timezone, pump_seconds, profile_store(profile).get("timezone"), now, cfg.home)


def profile_store(profile: dict[str, Any] | None) -> dict[str, Any]:
    if not profile:
        return {}
    store = profile.get("store") or {}
    return store.get(profile.get("defaultProfile")) or next(iter(store.values()), {})


async def context(ns: Nightscout, timezone: str | None, now: datetime, cfg: Config,
                  ) -> tuple[dict[str, Any] | None, dict[str, Any] | None, timefmt.Zone]:
    statuses, profile = await asyncio.gather(ns.devicestatus(1), ns.profile())
    ds = statuses[0] if statuses else None
    return ds, profile, zone_for(timezone, ds, profile, now, cfg)


def pump_zone_mismatch(zone: timefmt.Zone, ds: dict[str, Any] | None, now: datetime) -> str | None:
    """When the caller's zone differs from the pump's, Loop's schedules still run on the pump's clock."""
    seconds = num(((ds or {}).get("pump") or {}).get("secondsFromGMT"))
    if zone.source != "argument" or seconds is None:
        return None
    pump = timedelta(seconds=seconds)
    if now.astimezone(zone.tz).utcoffset() == pump:
        return None
    return (f"The pump is on {timefmt.offset_label(pump)}, not {zone.name}: Loop runs its schedules on the pump "
            "clock until the pump time is synced in Loop.")


async def current_status(ns: Nightscout, now: datetime, cfg: Config, timezone: str | None = None) -> dict[str, Any]:
    (ds, profile, zone), entries = await asyncio.gather(context(ns, timezone, now, cfg), ns.entries(1))
    out: dict[str, Any] = {"time_zone": zone.describe(now), "now": timefmt.when(now, now, zone)}
    warnings = [w for w in [pump_zone_mismatch(zone, ds, now)] if w]

    entry = entries[0] if entries else None
    if entry and num(entry.get("sgv")) is not None and (t := timefmt.parse(entry.get("date"))):
        out["glucose"] = compact({
            "mg_dl": entry["sgv"],
            "trend": TRENDS.get(entry.get("direction"), entry.get("direction")),
            "rate_mg_dl_per_minute": num(entry.get("trendRate")),
            "time": timefmt.when(t, now, zone),
            "device": entry.get("device"),
        })
        if now - t > STALE:
            warnings.append(f"The newest glucose reading is {timefmt.relative(t - now)}: the CGM or phone is not "
                            "uploading.")
    else:
        warnings.append("Nightscout has no glucose readings.")

    ds = ds or {}
    loop = ds.get("loop") or {}
    if (t := timefmt.parse(loop.get("timestamp"))):
        predicted = loop.get("predicted") or {}
        values = [v for v in (predicted.get("values") or []) if num(v) is not None]
        start = timefmt.parse(predicted.get("startDate"))
        forecast = None
        if values and start:
            low = min(range(len(values)), key=values.__getitem__)
            forecast = {
                "eventual_mg_dl": round(values[-1]),
                "eventual_at": timefmt.when(start + timedelta(minutes=5 * (len(values) - 1)), now, zone),
                "min_mg_dl": round(values[low]),
                "min_at": timefmt.when(start + timedelta(minutes=5 * low), now, zone),
            }
        enacted = loop.get("enacted") or {}
        out["loop"] = compact({
            "last_run": timefmt.when(t, now, zone),
            "version": loop.get("version"),
            "iob_units": round(v, 2) if (v := num((loop.get("iob") or {}).get("iob"))) is not None else None,
            "cob_grams": round(v) if (v := num((loop.get("cob") or {}).get("cob"))) is not None else None,
            "recommended_bolus_units": num(loop.get("recommendedBolus")),
            "forecast": forecast,
            "last_enacted": compact({
                "temp_basal_units_per_hour": num(enacted.get("rate")),
                "duration_minutes": num(enacted.get("duration")),
                "automatic_bolus_units": num(enacted.get("bolusVolume")),
            }) or None,
            "failure": loop.get("failureReason"),
        })
        if now - t > STALE:
            warnings.append(f"Loop last reported {timefmt.relative(t - now)}.")
    else:
        warnings.append("Nightscout has no Loop status.")

    pump = ds.get("pump") or {}
    if pump:
        out["pump"] = compact({
            "model": " ".join(x for x in (pump.get("manufacturer"), pump.get("model")) if x) or None,
            "suspended": pump.get("suspended"),
            "bolusing": pump.get("bolusing"),
            "reservoir_units": num(pump.get("reservoir")),
            "reservoir_note": None if num(pump.get("reservoir")) is not None
            else "Not reported; an Omnipod reports its reservoir only below 50 U.",
        })
    override = ds.get("override") or {}
    if override:
        target = override.get("currentCorrectionRange") or {}
        out["override"] = compact({
            "active": override.get("active"),
            "name": override.get("name"),
            "insulin_needs_percent": round(100 * m) if (m := num(override.get("multiplier"))) is not None else None,
            "target_mg_dl": [target.get("minValue"), target.get("maxValue")] if target else None,
            "ends": timefmt.when(e, now, zone) if (s := timefmt.parse(override.get("timestamp")))
            and (d := num(override.get("duration"))) and override.get("active")
            and (e := s + timedelta(seconds=d)) else None,
        })
    uploader = ds.get("uploader") or {}
    if uploader:
        out["phone"] = compact({"battery_percent": num(uploader.get("battery")),
                                "charging": uploader.get("isCharging")})
    if warnings:
        out["warnings"] = warnings
    return out


def lifetime(start: datetime, hours: float, grace_hours: float, now: datetime, zone: timefmt.Zone) -> dict[str, Any]:
    expires = start + timedelta(hours=hours)
    grace_ends = expires + timedelta(hours=grace_hours)
    status = "active" if now < expires else "expired, in grace period" if now < grace_ends else "past grace period"
    return {
        "started": timefmt.when(start, now, zone),
        "expires": timefmt.when(expires, now, zone),
        "grace_ends": timefmt.when(grace_ends, now, zone),
        "status": status,
        "lifetime_hours": hours,
        "grace_hours": grace_hours,
    }


async def device_lifetimes(ns: Nightscout, now: datetime, cfg: Config, timezone: str | None = None) -> dict[str, Any]:
    (ds, profile, zone), site, sensor, entries = await asyncio.gather(
        context(ns, timezone, now, cfg), ns.latest_treatment("Site Change"), ns.latest_treatment("Sensor Start"),
        ns.entries(1))
    out: dict[str, Any] = {"time_zone": zone.describe(now), "now": timefmt.when(now, now, zone)}
    pump = (ds or {}).get("pump") or {}

    if site and (t := timefmt.parse(site.get("created_at") or site.get("timestamp"))):
        out["pod"] = compact({
            "model": " ".join(x for x in (pump.get("manufacturer"), pump.get("model")) if x) or None,
            **lifetime(t, cfg.pod_hours, cfg.pod_grace_hours, now, zone),
        })
    else:
        out["pod"] = {"note": "Nightscout has no Site Change event."}

    if sensor and (t := timefmt.parse(sensor.get("created_at") or sensor.get("timestamp"))):
        device = (entries[0].get("device") if entries else None)
        out["cgm"] = compact({"device": device, **lifetime(t, cfg.cgm_hours, cfg.cgm_grace_hours, now, zone)})
    else:
        out["cgm"] = {"note": "Nightscout has no Sensor Start event."}

    if (version := ((ds or {}).get("loop") or {}).get("version")):
        out["loop"] = {"version": version}
    if (w := pump_zone_mismatch(zone, ds, now)):
        out["warnings"] = [w]
    return out


def window(hours: float, end: str | None, now: datetime) -> tuple[datetime, datetime]:
    if not 0 < hours <= MAX_HOURS:
        raise ValueError(f"hours must be between 0 and {MAX_HOURS}")
    stop = now
    if end:
        stop = timefmt.parse(end)
        if stop is None:
            raise ValueError(f"cannot parse end {end!r}: pass ISO 8601, e.g. 2026-11-24T08:00:00-08:00")
    return stop - timedelta(hours=hours), stop


async def glucose_history(ns: Nightscout, now: datetime, cfg: Config, hours: float = 24, end: str | None = None,
                          timezone: str | None = None, max_points: int = 300) -> dict[str, Any]:
    start, stop = window(hours, end, now)
    (_, _, zone), entries = await asyncio.gather(
        context(ns, timezone, now, cfg), ns.entries(int(hours * 12) + 50, start, stop))
    readings = sorted(
        ((t, e["sgv"]) for e in entries
         if num(e.get("sgv")) is not None and (t := timefmt.parse(e.get("date"))) and start <= t <= stop),
        key=lambda r: r[0])
    points = stats.downsample(readings, max_points)
    return compact({
        "time_zone": zone.describe(now),
        "from": timefmt.when(start, now, zone),
        "to": timefmt.when(stop, now, zone),
        "summary": stats.glucose_stats([v for _, v in readings], hours * 60),
        "readings_note": f"{len(points)} of {len(readings)} readings, evenly spaced" if len(points) < len(readings)
        else None,
        "readings": [{"local": t.astimezone(zone.tz).strftime("%a %-d %H:%M"), "mg_dl": v} for t, v in points],
    })


def treatment_row(t: dict[str, Any], when: dict[str, str]) -> dict[str, Any]:
    scale = num(t.get("insulinNeedsScaleFactor"))
    return compact({
        "time": when,
        "event": t.get("eventType"),
        "insulin_units": num(t.get("insulin")),
        "carbs_grams": num(t.get("carbs")),
        "food": t.get("foodType"),
        "absorption_hours": round(a / 60, 1) if (a := num(t.get("absorptionTime"))) is not None else None,
        "automatic": t.get("automatic"),
        "rate_units_per_hour": num(t.get("absolute")) if t.get("eventType") == TEMP_BASAL else None,
        "duration_minutes": round(d, 1) if (d := num(t.get("duration"))) is not None
        and t.get("eventType") != "Correction Bolus" else None,
        "insulin_needs_percent": round(100 * scale) if scale is not None else None,
        "reason": t.get("reason"),
        "notes": t.get("notes"),
    })


async def treatments(ns: Nightscout, now: datetime, cfg: Config, hours: float = 24, end: str | None = None,
                     event_type: str | None = None, include_temp_basals: bool = False,
                     timezone: str | None = None) -> dict[str, Any]:
    start, stop = window(hours, end, now)
    exclude = None if include_temp_basals or event_type else TEMP_BASAL
    (_, _, zone), found = await asyncio.gather(
        context(ns, timezone, now, cfg), ns.treatments(5000, start, stop, event_type, exclude))
    rows: list[tuple[datetime, dict[str, Any]]] = []
    days: dict[str, dict[str, float]] = defaultdict(lambda: {"bolus_units": 0.0, "carbs_grams": 0.0})
    for t in found:
        at = timefmt.parse(t.get("created_at") or t.get("timestamp"))
        if at is None or not start <= at <= stop:
            continue
        rows.append((at, treatment_row(t, timefmt.when(at, now, zone))))
        day = days[at.astimezone(zone.tz).strftime("%a %b %-d")]
        if t.get("eventType") != TEMP_BASAL and (i := num(t.get("insulin"))):
            day["bolus_units"] += i
        if (c := num(t.get("carbs"))):
            day["carbs_grams"] += c
    rows.sort(key=lambda r: r[0])
    return {
        "time_zone": zone.describe(now),
        "from": timefmt.when(start, now, zone),
        "to": timefmt.when(stop, now, zone),
        "daily_totals": {d: {k: round(v, 2) for k, v in t.items()} for d, t in days.items()},
        "totals_note": "Bolus insulin only (manual and automatic); basal insulin is not included."
        + ("" if exclude is None else " Temp basals are left out; pass include_temp_basals to list them."),
        "treatments": [r for _, r in rows],
    }


def schedule(items: Any) -> list[dict[str, Any]]:
    return [{"start": i.get("time"), "value": i.get("value")} for i in (items or []) if isinstance(i, dict)]


async def therapy_settings(ns: Nightscout, now: datetime, cfg: Config, timezone: str | None = None) -> dict[str, Any]:
    ds, profile, zone = await context(ns, timezone, now, cfg)
    if not profile:
        return {"note": "Nightscout has no profile."}
    store = profile_store(profile)
    schedule_zone = timefmt.resolve(None, None, store.get("timezone"), now, cfg.home)
    units = store.get("units") or profile.get("units")
    loop = profile.get("loopSettings") or {}
    presets = [compact({
        "name": p.get("name"),
        "target_mg_dl": p.get("targetRange"),
        "insulin_needs_percent": round(100 * s) if (s := num(p.get("insulinNeedsScaleFactor"))) is not None else None,
        "duration_minutes": round(d / 60) if (d := num(p.get("duration"))) else None,
    }) for p in loop.get("overridePresets") or [] if isinstance(p, dict)]
    out = compact({
        "time_zone": zone.describe(now),
        "active_since": timefmt.when(s, now, zone) if (s := timefmt.parse(profile.get("startDate"))) else None,
        "units": units,
        "schedule_time_zone": compact({"profile": store.get("timezone"),
                                       "utc_offset": schedule_zone.describe(now)["utc_offset"]}),
        "insulin_action_hours": num(store.get("dia")),
        "basal_units_per_hour": schedule(store.get("basal")),
        "insulin_sensitivity_per_unit": schedule(store.get("sens")),
        "carb_ratio_grams_per_unit": schedule(store.get("carbratio")),
        "target_low": schedule(store.get("target_low")),
        "target_high": schedule(store.get("target_high")),
        "loop": compact({k: loop.get(k) for k in LOOP_SETTINGS}) or None,
        "override_presets": presets or None,
    })
    if (w := pump_zone_mismatch(zone, ds, now)):
        out["warnings"] = [w]
    return out
