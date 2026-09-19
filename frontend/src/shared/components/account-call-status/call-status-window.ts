export const CHART_PX_PER_POINT = 28;
const MIN_VISIBLE_POINTS = 8;
const FALLBACK_VISIBLE_POINTS = 24;

export function chartWindow(pointCount: number, hostWidth: number, pxPerPoint = CHART_PX_PER_POINT): {
  visibleCount: number;
  maxStart: number;
} {
  if (pointCount <= 0) return { visibleCount: 0, maxStart: 0 };
  const fitted = hostWidth > 0 ? Math.floor(hostWidth / pxPerPoint) : FALLBACK_VISIBLE_POINTS;
  const visibleCount = Math.min(pointCount, Math.max(MIN_VISIBLE_POINTS, fitted || MIN_VISIBLE_POINTS));
  return { visibleCount, maxStart: Math.max(0, pointCount - visibleCount) };
}

export function clampChartStart(start: number, maxStart: number): number {
  if (maxStart <= 0) return 0;
  return Math.min(maxStart, Math.max(0, start));
}

export function startForHighlight(index: number, visibleCount: number, pointCount: number): number {
  if (index < 0 || visibleCount <= 0) return Math.max(0, pointCount - visibleCount);
  return clampChartStart(index - Math.floor(visibleCount / 2), Math.max(0, pointCount - visibleCount));
}
