import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { getRequestAudit, type AuditDTO, type AuditPeriod } from "@/features/audits/request-audits-api";
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
      <h2 className="text-xs font-medium">{t("audits.requestTrend")}</h2>
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
    </section>
  );
}
