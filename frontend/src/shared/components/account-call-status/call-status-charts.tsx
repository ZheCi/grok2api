import { useLayoutEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { useTranslation } from "react-i18next";
import { CartesianGrid, Line, LineChart, ReferenceLine, XAxis, YAxis } from "recharts";

import { ChartContainer, ChartTooltip, type ChartConfig } from "@/components/ui/chart";
import type { CallLevelName, CallStatusPointDTO, CallStatusThresholdsDTO } from "@/shared/components/account-call-status/account-call-status-api";
import { formatCallStatusTick } from "@/shared/components/account-call-status/call-status-tick";
import { chartWindow, clampChartStart, startForHighlight } from "@/shared/components/account-call-status/call-status-window";
import { cn } from "@/shared/lib/cn";
import { formatCompactDateTime, formatDateTime, formatDuration, formatNumber } from "@/shared/lib/format";

export type CallStatusChartPoint = CallStatusPointDTO & { index: number; label: string; levelValue: number; highlight: boolean };

const LEVEL_VALUE: Record<CallLevelName, number> = { failed: 1, degraded: 2, soft: 3, healthy: 4 };
export const CALL_LEVEL_COLOR: Record<CallLevelName, string> = {
  failed: "oklch(0.63 0.2 25)",
  degraded: "oklch(0.7 0.17 50)",
  soft: "oklch(0.75 0.15 85)",
  healthy: "oklch(0.7 0.15 155)",
};
const LEVEL_ORDER: CallLevelName[] = ["failed", "degraded", "soft", "healthy"];
const DOUBLE_CLICK_MS = 400;

export function CallStatusCharts({
  points,
  totals,
  thresholds,
  locale,
  highlightId,
  onOpenAudit,
  tickStyle = "time",
  showStats = true,
  showList = true,
}: {
  points: CallStatusPointDTO[];
  totals: { calls: number; healthy: number; soft: number; degraded: number; failed: number; maxTPS: number };
  thresholds: CallStatusThresholdsDTO;
  locale: string;
  highlightId?: string;
  onOpenAudit?: (auditId: string) => void;
  tickStyle?: "time" | "datetime";
  showStats?: boolean;
  showList?: boolean;
}) {
  const { t } = useTranslation();
  const lastClick = useRef<{ id: string; at: number } | null>(null);
  const hostRef = useRef<HTMLDivElement>(null);
  const maxStartRef = useRef(0);
  const startRef = useRef(0);
  const userPanned = useRef(false);
  const [hostWidth, setHostWidth] = useState(0);
  const [start, setStart] = useState(0);
  const tpsCeiling = Math.max(thresholds.hardTPS, thresholds.softTPS, totals.maxTPS, 1);
  const chartData = useMemo<CallStatusChartPoint[]>(() => points.map((point, index) => ({
    ...point,
    index,
    label: formatCallStatusTick(point.t, locale, tickStyle),
    levelValue: LEVEL_VALUE[point.level],
    highlight: highlightId === point.id,
  })), [highlightId, locale, points, tickStyle]);
  const { visibleCount, maxStart } = chartWindow(chartData.length, hostWidth);
  maxStartRef.current = maxStart;
  const windowStart = clampChartStart(start, maxStart);
  startRef.current = windowStart;
  const windowed = useMemo(
    () => chartData.slice(windowStart, windowStart + visibleCount).map((point, index) => ({ ...point, index })),
    [chartData, visibleCount, windowStart],
  );
  const labelStep = windowed.length > 12 ? Math.ceil(windowed.length / 8) : 1;
  const ticks = windowed.map((point) => point.index).filter((index) => index % labelStep === 0 || index === windowed.length - 1);
  const lastPointId = chartData.at(-1)?.id;
  const seriesKey = `${highlightId ?? ""}:${lastPointId ?? ""}:${chartData.length}`;

  useLayoutEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const update = () => setHostWidth(el.clientWidth);
    update();
    const observer = new ResizeObserver(update);
    observer.observe(el);
    const panBy = (delta: number): boolean => {
      const limit = maxStartRef.current;
      if (limit <= 0 || delta === 0) return false;
      const move = Math.sign(delta) * Math.max(1, Math.round(Math.abs(delta) / 28));
      const next = clampChartStart(startRef.current + move, limit);
      if (next === startRef.current) return false;
      userPanned.current = true;
      setStart(next);
      return true;
    };
    const onWheel = (event: WheelEvent) => {
      const delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
      if (panBy(delta)) event.preventDefault();
    };
    let dragX: number | null = null;
    let dragging = false;
    const onPointerDown = (event: PointerEvent) => {
      if (maxStartRef.current <= 0 || (event.pointerType === "mouse" && event.button !== 0)) return;
      if ((event.target as HTMLElement | null)?.closest("[role='scrollbar']")) return;
      dragX = event.clientX;
      dragging = false;
    };
    const onPointerMove = (event: PointerEvent) => {
      if (dragX === null) return;
      const dx = dragX - event.clientX;
      if (!dragging && Math.abs(dx) < 8) return;
      if (!dragging) {
        dragging = true;
        el.setPointerCapture(event.pointerId);
      }
      if (panBy(dx)) dragX = event.clientX;
    };
    const onPointerUp = () => {
      dragX = null;
      dragging = false;
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    el.addEventListener("pointerdown", onPointerDown);
    el.addEventListener("pointermove", onPointerMove);
    el.addEventListener("pointerup", onPointerUp);
    el.addEventListener("pointercancel", onPointerUp);
    return () => {
      observer.disconnect();
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("pointerdown", onPointerDown);
      el.removeEventListener("pointermove", onPointerMove);
      el.removeEventListener("pointerup", onPointerUp);
      el.removeEventListener("pointercancel", onPointerUp);
    };
  }, []);

  useLayoutEffect(() => {
    userPanned.current = false;
  }, [seriesKey]);

  useLayoutEffect(() => {
    if (highlightId && !userPanned.current) {
      const index = chartData.findIndex((point) => point.id === highlightId);
      setStart(startForHighlight(index, visibleCount, chartData.length));
      return;
    }
    if (!userPanned.current) {
      setStart(maxStart);
      return;
    }
    setStart((current) => clampChartStart(current, maxStart));
  }, [chartData, highlightId, maxStart, seriesKey, visibleCount]);
  const chartConfig = useMemo<ChartConfig>(() => ({
    levelValue: { label: t("audits.callStatusQualityChart"), theme: { light: "oklch(0.45 0.02 250)", dark: "oklch(0.8 0.02 250)" } },
    tps: { label: t("audits.callStatusTpsChart"), theme: { light: "oklch(0.62 0.14 245)", dark: "oklch(0.74 0.12 245)" } },
  }), [t]);
  const last = showStats ? points[points.length - 1] : undefined;

  function openOnDoubleClick(id?: string): void {
    if (!id || !onOpenAudit) return;
    const now = Date.now();
    if (lastClick.current?.id === id && now - lastClick.current.at < DOUBLE_CLICK_MS) {
      lastClick.current = null;
      onOpenAudit(id);
      return;
    }
    lastClick.current = { id, at: now };
  }

  return (
    <div className="space-y-2">
      {showStats && last ? (
        <dl className="grid grid-cols-2 gap-1.5 sm:grid-cols-3 xl:grid-cols-6">
          <Stat label={t("audits.callStatusCalls")} value={formatNumber(totals.calls, locale, 0)} />
          <Stat label={t("audits.callStatusHealthyShare")} value={`${formatNumber(totals.calls ? totals.healthy / totals.calls * 100 : 0, locale, 0)}%`} />
          <Stat label={t("audits.callStatusDegradedHits")} value={formatNumber(totals.degraded, locale, 0)} />
          <Stat label={t("audits.callStatusFailedHits")} value={formatNumber(totals.failed, locale, 0)} />
          <Stat label={t("audits.callStatusPeakTps")} value={formatNumber(totals.maxTPS, locale, 1)} />
          <Stat label={t("qualityGuard.lastObserved")} value={formatCompactDateTime(last.t, locale)} />
        </dl>
      ) : null}
      <div ref={hostRef} className={cn("space-y-1.5", maxStart > 0 && "cursor-ew-resize touch-pan-y select-none")}>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-muted-foreground">
          <span className="inline-flex items-center gap-1">
            <span className="h-px w-3 bg-[oklch(0.45_0.02_250)] dark:bg-[oklch(0.8_0.02_250)]" />
            {t("audits.callStatusQualityChart")}
          </span>
          <span className="inline-flex items-center gap-1">
            <span className="h-px w-3 bg-[oklch(0.68_0.13_245)]" />
            {t("audits.callStatusTpsChart")}
          </span>
          <span className="inline-flex items-center gap-1">
            <span className="h-px w-3 border-t border-dashed border-[oklch(0.75_0.15_85)]" />
            {t("qualityGuard.softThreshold")}
          </span>
          <span className="inline-flex items-center gap-1">
            <span className="h-px w-3 border-t border-dashed border-[oklch(0.63_0.2_25)]" />
            {t("qualityGuard.hardThreshold")}
          </span>
        </div>
        <ChartContainer config={chartConfig} className="h-[168px] w-full aspect-auto">
          <LineChart data={windowed} margin={{ left: 0, right: 8, top: 2, bottom: 0 }} onClick={(state) => openOnDoubleClick((state?.activePayload?.[0]?.payload as CallStatusChartPoint | undefined)?.id)}>
            <CartesianGrid vertical={false} strokeDasharray="3 3" />
            <XAxis dataKey="index" type="number" domain={[0, Math.max(windowed.length - 1, 0)]} ticks={ticks} tickLine={false} axisLine={false} tickMargin={6} tickFormatter={(value) => windowed[Number(value)]?.label ?? ""} />
            <YAxis yAxisId="level" domain={[1, 4]} ticks={[1, 2, 3, 4]} tickLine={false} axisLine={false} width={44} tickFormatter={(value) => t(`audits.callStatusLevels.${LEVEL_ORDER[Number(value) - 1] ?? "failed"}`)} />
            <YAxis yAxisId="tps" orientation="right" domain={[0, tpsCeiling]} tickLine={false} axisLine={false} width={56} tickMargin={4} tickFormatter={(value) => formatNumber(Number(value), locale, 0)} />
            <ReferenceLine yAxisId="tps" y={thresholds.softTPS} stroke="oklch(0.75 0.15 85)" strokeDasharray="4 4" />
            <ReferenceLine yAxisId="tps" y={thresholds.hardTPS} stroke="oklch(0.63 0.2 25)" strokeDasharray="4 4" />
            <ChartTooltip cursor={{ strokeDasharray: "3 3" }} content={({ active, payload }) => {
              const point = payload?.[0]?.payload as CallStatusChartPoint | undefined;
              if (!active || !point) return null;
              return <PointTooltip point={point} locale={locale} />;
            }} />
            <Line yAxisId="tps" type="linear" dataKey="tps" stroke="var(--color-tps)" strokeWidth={1.5} dot={false} activeDot={false} isAnimationActive={false} />
            <Line yAxisId="level" type="linear" dataKey="levelValue" stroke="var(--color-levelValue)" strokeWidth={1.5} dot={ChartDot} activeDot={{ r: 4 }} isAnimationActive={false} />
          </LineChart>
        </ChartContainer>
        {maxStart > 0 ? (
          <ChartRangeBar
            start={windowStart}
            visibleCount={visibleCount}
            total={chartData.length}
            onStartChange={(next) => {
              userPanned.current = true;
              setStart(next);
            }}
          />
        ) : null}
      </div>
      {showList ? (
        <section className="space-y-1.5">
          <h3 className="text-xs font-medium">{t("audits.callStatusCallList")}</h3>
          <div className="overflow-hidden rounded-md border">
            <table className="w-full text-left">
              <thead className="bg-muted/40 text-[10px] text-muted-foreground">
                <tr>
                  <th className="px-3 py-1.5 font-medium">{t("audits.createdAt")}</th>
                  <th className="px-3 py-1.5 font-medium">{t("audits.callStatusQuality")}</th>
                  <th className="px-3 py-1.5 font-medium">{t("audits.throughputMetric")}</th>
                  <th className="px-3 py-1.5 font-medium">{t("audits.durationMetric")}</th>
                  <th className="px-3 py-1.5 font-medium">{t("audits.model")}</th>
                  <th className="px-3 py-1.5 font-medium">{t("audits.egress")}</th>
                </tr>
              </thead>
              <tbody>
                {[...chartData].reverse().map((point) => (
                  <tr
                    key={point.id}
                    className={cn("border-t", onOpenAudit && "cursor-pointer hover:bg-muted/40", point.highlight && "bg-muted/60")}
                    onDoubleClick={() => onOpenAudit?.(point.id)}
                  >
                    <td className="whitespace-nowrap px-3 py-1.5 font-mono tabular-nums">{formatCompactDateTime(point.t, locale)}</td>
                    <td className="px-3 py-1.5">
                      <span className="inline-flex items-center gap-1.5">
                        <span className="size-1.5 rounded-full" style={{ backgroundColor: CALL_LEVEL_COLOR[point.level] }} />
                        {t(`audits.callStatusLevels.${point.level}`)}
                        {point.class ? <span className="text-muted-foreground">{t(`audits.callStatusClasses.${point.class}`, { defaultValue: point.class })}</span> : null}
                      </span>
                    </td>
                    <td className="px-3 py-1.5 font-mono tabular-nums">{formatNumber(point.tps, locale, 1)}</td>
                    <td className="px-3 py-1.5 font-mono tabular-nums">{formatDuration(point.durationMs)}</td>
                    <td className="max-w-32 truncate px-3 py-1.5">{point.model || "—"}</td>
                    <td className="max-w-28 truncate px-3 py-1.5">{point.nodeName || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}
    </div>
  );
}

function ChartRangeBar({
  start,
  visibleCount,
  total,
  onStartChange,
}: {
  start: number;
  visibleCount: number;
  total: number;
  onStartChange: (start: number) => void;
}) {
  const { t } = useTranslation();
  const trackRef = useRef<HTMLDivElement>(null);
  const maxStart = Math.max(0, total - visibleCount);
  const thumbWidth = total > 0 ? Math.max((visibleCount / total) * 100, 12) : 100;
  const thumbLeft = maxStart > 0 ? (start / maxStart) * (100 - thumbWidth) : 0;

  function startFromClientX(clientX: number): number {
    const track = trackRef.current;
    if (!track || maxStart <= 0) return 0;
    const rect = track.getBoundingClientRect();
    const thumbPx = (thumbWidth / 100) * rect.width;
    const usable = Math.max(rect.width - thumbPx, 1);
    const ratio = (clientX - rect.left - thumbPx / 2) / usable;
    return clampChartStart(Math.round(ratio * maxStart), maxStart);
  }

  function onPointerDown(event: ReactPointerEvent<HTMLDivElement>): void {
    event.currentTarget.setPointerCapture(event.pointerId);
    onStartChange(startFromClientX(event.clientX));
  }

  function onPointerMove(event: ReactPointerEvent<HTMLDivElement>): void {
    if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
    onStartChange(startFromClientX(event.clientX));
  }

  return (
    <div
      ref={trackRef}
      role="scrollbar"
      aria-label={t("audits.callStatusScrollHint")}
      aria-orientation="horizontal"
      aria-valuemin={0}
      aria-valuemax={maxStart}
      aria-valuenow={start}
      tabIndex={0}
      className="relative h-2 cursor-pointer select-none rounded-full bg-muted"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onKeyDown={(event) => {
        if (event.key === "ArrowLeft") {
          event.preventDefault();
          onStartChange(clampChartStart(start - 1, maxStart));
        }
        if (event.key === "ArrowRight") {
          event.preventDefault();
          onStartChange(clampChartStart(start + 1, maxStart));
        }
        if (event.key === "Home") {
          event.preventDefault();
          onStartChange(0);
        }
        if (event.key === "End") {
          event.preventDefault();
          onStartChange(maxStart);
        }
      }}
    >
      <div
        className="absolute top-0 h-2 rounded-full bg-foreground/35 hover:bg-foreground/50"
        style={{ left: `${thumbLeft}%`, width: `${thumbWidth}%` }}
      />
    </div>
  );
}

function ChartDot({ cx = 0, cy = 0, payload }: { cx?: number; cy?: number; payload?: CallStatusChartPoint }) {
  const highlight = Boolean(payload?.highlight);
  return (
    <circle
      cx={cx}
      cy={cy}
      r={highlight ? 4 : 2.5}
      fill={CALL_LEVEL_COLOR[payload?.level ?? "healthy"]}
      stroke={highlight ? "var(--foreground)" : "var(--background)"}
      strokeWidth={highlight ? 2 : 1}
    />
  );
}

function PointTooltip({ point, locale }: { point: CallStatusPointDTO; locale: string }) {
  const { t } = useTranslation();
  return (
    <div className="grid min-w-56 gap-1 rounded-md border bg-background px-3 py-2 text-[11px] shadow-md">
      <div className="font-medium">{formatDateTime(point.t, locale)}</div>
      <div>{t(`audits.callStatusLevels.${point.level}`)} · HTTP {point.statusCode || "-"}</div>
      {point.class ? <div>{t(`audits.callStatusClasses.${point.class}`, { defaultValue: point.class })}</div> : null}
      {point.errorCode ? <div>{point.errorCode}</div> : null}
      <div>{formatNumber(point.tps, locale, 1)} Token/s · {formatDuration(point.durationMs)}</div>
      {point.model ? <div>{point.model}</div> : null}
      {point.accountName ? <div>{point.accountName}</div> : null}
      {point.nodeName ? <div>{point.nodeName}</div> : null}
      <div className="font-mono text-muted-foreground">{point.requestId}</div>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md bg-muted/40 px-2.5 py-1.5">
      <dt className="text-[10px] text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 truncate font-medium tabular-nums">{value}</dd>
    </div>
  );
}
