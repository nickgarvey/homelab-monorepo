"""Glucose summary statistics, with the usual consensus ranges (mg/dL)."""

from __future__ import annotations

import statistics
from typing import Any


def pct(n: int, total: int) -> float:
    return round(100 * n / total, 1)


def glucose_stats(values: list[float], span_minutes: float, interval_minutes: float = 5.0) -> dict[str, Any] | None:
    if not values:
        return None
    n = len(values)
    mean = statistics.fmean(values)
    sd = statistics.pstdev(values) if n > 1 else 0.0
    expected = max(1, int(span_minutes // interval_minutes))
    return {
        "readings": n,
        "coverage_percent": min(100.0, pct(n, expected)),
        "mean_mg_dl": round(mean, 1),
        "sd_mg_dl": round(sd, 1),
        "cv_percent": round(100 * sd / mean, 1) if mean else None,
        # Bergenstal et al. 2018: GMI (%) = 3.31 + 0.02392 * mean glucose (mg/dL).
        "gmi_percent": round(3.31 + 0.02392 * mean, 1),
        "min_mg_dl": min(values),
        "max_mg_dl": max(values),
        "time_below_54_percent": pct(sum(v < 54 for v in values), n),
        "time_below_70_percent": pct(sum(v < 70 for v in values), n),
        "time_in_range_70_180_percent": pct(sum(70 <= v <= 180 for v in values), n),
        "time_above_180_percent": pct(sum(v > 180 for v in values), n),
        "time_above_250_percent": pct(sum(v > 250 for v in values), n),
    }


def downsample(items: list[Any], limit: int) -> list[Any]:
    """At most `limit` items, evenly spaced, always keeping the first and last."""
    if len(items) <= limit:
        return items
    step = (len(items) - 1) / (limit - 1)
    return [items[round(i * step)] for i in range(limit)]
