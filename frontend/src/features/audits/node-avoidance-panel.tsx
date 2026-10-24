import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { getNodeAvoidance, type NodeAvoidanceItem } from "@/features/audits/node-avoidance-api";
import { CALL_LEVEL_COLOR } from "@/shared/components/account-call-status/call-status-charts";

const HEALTHY_COLOR = "oklch(0.7 0.15 155)";
const DISABLED_COLOR = "oklch(0.62 0.01 250)";
const AVOID_COLOR = CALL_LEVEL_COLOR.degraded;

export function formatAvoidRemaining(ms: number): string {
  const total = Math.max(1, Math.ceil(ms / 1000));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  const clock = `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
  return hours > 0 ? `${hours}:${clock}` : `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function remainingMs(item: NodeAvoidanceItem, now: number): number {
  if (item.status !== "avoiding" || !item.avoidUntil) return 0;
  const until = Date.parse(item.avoidUntil);
  if (!Number.isFinite(until)) return 0;
  return Math.max(0, until - now);
}

function compareNodes(left: NodeAvoidanceItem, right: NodeAvoidanceItem, now: number): number {
  const remaining = remainingMs(right, now) - remainingMs(left, now);
  if (remaining !== 0) return remaining;
  const rank = (item: NodeAvoidanceItem) => (remainingMs(item, now) > 0 ? 0 : item.status === "disabled" ? 2 : 1);
  const byStatus = rank(left) - rank(right);
  if (byStatus !== 0) return byStatus;
  return left.name.localeCompare(right.name) || left.id.localeCompare(right.id);
}

export function NodeAvoidancePanel({ active }: { active: boolean }) {
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Date.now());
  const query = useQuery({
    queryKey: ["egress-nodes", "avoidance"],
    queryFn: ({ signal }) => getNodeAvoidance(signal),
    enabled: active,
    refetchInterval: active ? 10_000 : false,
    staleTime: 10_000,
  });

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  const items = [...(query.data?.items ?? [])].sort((left, right) => compareNodes(left, right, now));

  return (
    <div className="flex h-[220px] min-w-0 flex-col border-l pl-2">
      <h3 className="mb-1 text-[11px] font-medium text-muted-foreground">{t("audits.nodeAvoidance")}</h3>
      {query.isError ? <p className="text-[11px] text-muted-foreground">{query.error.message}</p> : null}
      {query.isPending && !query.data ? <p className="text-[11px] text-muted-foreground">{t("common.loading")}</p> : null}
      {query.data && items.length === 0 ? <p className="text-[11px] text-muted-foreground">{t("audits.nodeAvoidanceEmpty")}</p> : null}
      {items.length > 0 ? (
        <ul className="min-h-0 flex-1 space-y-1 overflow-y-auto pr-1">
          {items.map((item) => {
            const remaining = remainingMs(item, now);
            const avoiding = remaining > 0;
            const disabled = !avoiding && item.status === "disabled";
            const color = avoiding ? AVOID_COLOR : disabled ? DISABLED_COLOR : HEALTHY_COLOR;
            const label = avoiding ? formatAvoidRemaining(remaining) : t(disabled ? "audits.nodeAvoidDisabled" : "audits.nodeAvoidHealthy");
            return (
              <li key={item.id} className="flex items-center gap-1.5 text-[11px]">
                <span className="size-1.5 shrink-0 rounded-full" style={{ backgroundColor: color }} />
                <span className="min-w-0 flex-1 truncate" title={item.name}>{item.name}</span>
                <span className="shrink-0 tabular-nums" style={{ color }}>{label}</span>
              </li>
            );
          })}
        </ul>
      ) : null}
    </div>
  );
}
