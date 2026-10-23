import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { getRequestAudit, type AuditDTO, type AuditPeriod } from "@/features/audits/request-audits-api";
import { NodeAvoidancePanel } from "@/features/audits/node-avoidance-panel";
import { getRequestCallStatus, type CallStatusThresholdInput } from "@/shared/components/account-call-status/account-call-status-api";
import { CallStatusCharts } from "@/shared/components/account-call-status/call-status-charts";
import { EmptyState, ErrorState, LoadingState } from "@/shared/components/data-state";

const AUDIT_TREND_CACHE_TIME_MS = 60_000;
const AUDIT_TREND_REFETCH_MS = 10_000;

export function RequestAuditTrend({
  period,
  search,
  model,
  status,
  mode,
  keyFilter,
  account,
  thresholds,
  onOpenAudit,
}: {
  period: AuditPeriod;
  search: string;
  model: string;
  status: string;
  mode: string;
  keyFilter: string;
  account: string;
  thresholds?: CallStatusThresholdInput;
  onOpenAudit: (audit: AuditDTO) => void;
}) {
  const { t, i18n } = useTranslation();
  const [avoidanceOpen, setAvoidanceOpen] = useState(true);
  const rowRef = useRef<HTMLDivElement>(null);
  const [panelWidth, setPanelWidth] = useState(0);
  useLayoutEffect(() => {
    const row = rowRef.current;
    if (!row) return;
    const update = () => setPanelWidth(Math.round(row.clientWidth * 0.2));
    update();
    const observer = new ResizeObserver(update);
    observer.observe(row);
    return () => observer.disconnect();
  }, []);
  const query = useQuery({
    queryKey: ["request-audits", "call-status", search, model, status, mode, keyFilter, account, period, thresholds?.softTPS, thresholds?.hardTPS, thresholds?.minGenMs, thresholds?.failClosed],
    queryFn: ({ signal }) => getRequestCallStatus({
      period, search, model, status, mode, key: keyFilter, account, thresholds,
    }, signal),
    placeholderData: (previous) => previous,
    refetchInterval: AUDIT_TREND_REFETCH_MS,
    staleTime: AUDIT_TREND_REFETCH_MS,
    gcTime: AUDIT_TREND_CACHE_TIME_MS,
  });
  const data = query.data;

  async function openAudit(auditId: string): Promise<void> {
    try {
      const detail = await getRequestAudit(auditId);
      onOpenAudit(detail.audit);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("common.retry"));
    }
  }

  return (
    <section className="space-y-2 rounded-lg bg-card p-3" aria-label={t("audits.requestTrend")}>
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-xs font-medium">{t("audits.requestTrend")}</h2>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-7"
          aria-expanded={avoidanceOpen}
          aria-label={t(avoidanceOpen ? "audits.nodeAvoidanceCollapse" : "audits.nodeAvoidanceExpand")}
          onClick={() => setAvoidanceOpen((open) => !open)}
        >
          {avoidanceOpen ? <ChevronRight className="size-4 transition-transform" /> : <ChevronLeft className="size-4 transition-transform" />}
        </Button>
      </div>
      <div ref={rowRef} className="flex min-w-0 items-start">
        <div className="min-w-0 flex-1">
          {query.isError ? <ErrorState message={query.error.message} onRetry={() => void query.refetch()} /> : null}
          {query.isPending && !data ? <LoadingState className="min-h-40" /> : null}
          {data?.truncated ? <p className="text-[11px] text-muted-foreground">{t("audits.callStatusTruncated", { count: data.points.length })}</p> : null}
          {data && data.points.length === 0 ? <EmptyState message={t("audits.requestTrendEmpty")} /> : null}
          {data && data.points.length > 0 ? (
            <CallStatusCharts
              points={data.points}
              totals={data.totals}
              thresholds={data.thresholds}
              locale={i18n.language}
              onOpenAudit={openAudit}
              tickStyle={period === "24h" ? "time" : "datetime"}
              showStats={false}
              showList={false}
            />
          ) : null}
        </div>
        <div className="shrink-0 overflow-hidden transition-[width] duration-300 ease-out" style={{ width: avoidanceOpen ? panelWidth : 0 }}>
          <div style={{ width: panelWidth }}>
            <NodeAvoidancePanel />
          </div>
        </div>
      </div>
    </section>
  );
}
