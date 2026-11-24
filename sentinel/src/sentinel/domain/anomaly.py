"""Statistical anomaly detection for golden signals.

Method: split a series into a *baseline* window and a *recent* window, then compute a robust
z-score of the recent mean against the baseline median using the Median Absolute Deviation
(MAD). MAD is insensitive to the spikes we are trying to find, unlike the standard deviation.

When the baseline is too short (cold start, new service) we fall back to static SLO-style
thresholds so the detector still produces a useful answer.
"""

from __future__ import annotations

import statistics
from dataclasses import dataclass

from sentinel.domain.models import Anomaly, Point, Signal, SignalKind

MAD_SCALE = 1.4826  # makes MAD a consistent estimator of sigma for normal data


@dataclass(frozen=True)
class SignalPolicy:
    direction: str  # "up", "down" or "both"
    min_abs: float  # observed value must exceed this to count (noise floor)
    min_ratio: float  # observed/baseline must exceed this for "up" anomalies
    static_threshold: float  # used when the baseline is too short
    unit: str


POLICIES: dict[SignalKind, SignalPolicy] = {
    SignalKind.ERROR_RATE: SignalPolicy("up", 0.02, 2.0, 0.05, "ratio"),
    SignalKind.LATENCY_P95: SignalPolicy("up", 0.2, 1.5, 0.5, "s"),
    SignalKind.REQUEST_RATE: SignalPolicy("both", 0.1, 2.0, float("inf"), "rps"),
}


def _fmt(kind: SignalKind, v: float) -> str:
    if kind is SignalKind.ERROR_RATE:
        return f"{v * 100:.1f}%"
    if kind is SignalKind.LATENCY_P95:
        return f"{v * 1000:.0f}ms"
    return f"{v:.2f}rps"


def _severity(kind: SignalKind, z: float, z_threshold: float, base: float, obs: float) -> float:
    """0..1 blend of statistical surprise and business impact.

    z alone saturates quickly (a flat baseline makes every spike "infinitely" surprising), so
    half of the score is impact: how far the value is past its SLO-style threshold (or, for a
    drop, the fraction of traffic lost).
    """
    surprise = min(abs(z) / (z_threshold * 3), 1.0)
    policy = POLICIES[kind]
    if z < 0:
        impact = min(max((base - obs) / base, 0.0), 1.0) if base > 0 else 0.0
    elif policy.static_threshold == float("inf"):
        impact = min(obs / base / 10, 1.0) if base > 0 else 0.5
    else:
        impact = min(obs / (policy.static_threshold * 4), 1.0)
    return round(0.5 * surprise + 0.5 * impact, 3)


def detect(
    signal: Signal,
    split_ts: float,
    z_threshold: float = 3.5,
    min_baseline_points: int = 6,
) -> Anomaly | None:
    """Return an :class:`Anomaly` if the recent part of ``signal`` deviates from its baseline."""
    policy = POLICIES[signal.kind]
    pts = [p for p in signal.points if p.value == p.value]  # drop NaN
    baseline = [p.value for p in pts if p.ts < split_ts]
    recent: list[Point] = [p for p in pts if p.ts >= split_ts]
    if not recent:
        return None
    observed = statistics.fmean(p.value for p in recent)

    if len(baseline) >= min_baseline_points:
        med = statistics.median(baseline)
        mad = statistics.median(abs(v - med) for v in baseline)
        # Floor the spread so a perfectly flat baseline does not produce infinite scores.
        spread = max(MAD_SCALE * mad, abs(med) * 0.05, 1e-3)
        z = (observed - med) / spread
        is_up = z >= z_threshold and observed >= policy.min_abs
        if is_up and med > 0:
            is_up = observed / med >= policy.min_ratio
        is_down = policy.direction == "both" and z <= -z_threshold and med >= policy.min_abs
        if not (is_up and policy.direction in {"up", "both"}) and not is_down:
            return None
        onset = next(
            (p.ts for p in recent if abs((p.value - med) / spread) >= z_threshold), recent[0].ts
        )
        return Anomaly(
            service=signal.service,
            kind=signal.kind,
            score=round(z, 2),
            severity=_severity(signal.kind, z, z_threshold, med, observed),
            baseline=med,
            observed=observed,
            onset_ts=onset,
            method="robust_z_mad",
            description=(
                f"{signal.kind.value} {'rose' if z > 0 else 'dropped'} to "
                f"{_fmt(signal.kind, observed)} (baseline {_fmt(signal.kind, med)}, z={z:.1f})"
            ),
        )

    # Cold-start fallback: static threshold.
    if observed >= policy.static_threshold and observed >= policy.min_abs:
        onset = next((p.ts for p in recent if p.value >= policy.static_threshold), recent[0].ts)
        ratio = observed / policy.static_threshold
        return Anomaly(
            service=signal.service,
            kind=signal.kind,
            score=round(ratio * z_threshold, 2),
            severity=round(min(ratio / 3, 1.0), 3),
            baseline=policy.static_threshold,
            observed=observed,
            onset_ts=onset,
            method="static_threshold",
            description=(
                f"{signal.kind.value} at {_fmt(signal.kind, observed)} exceeds static threshold "
                f"{_fmt(signal.kind, policy.static_threshold)} (insufficient baseline)"
            ),
        )
    return None


def detect_all(signals: list[Signal], split_ts: float, z_threshold: float = 3.5) -> list[Anomaly]:
    found = [a for s in signals if (a := detect(s, split_ts, z_threshold)) is not None]
    return sorted(found, key=lambda a: a.severity, reverse=True)
