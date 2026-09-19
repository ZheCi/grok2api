import { memo, type KeyboardEvent, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { CALL_LEVEL_CLASS, CALL_LEVEL_HEIGHT, type AccountCallStatusDTO } from "@/shared/components/account-call-status/account-call-status-api";
import { cn } from "@/shared/lib/cn";

const SPARKLINE_WIDTH = 72;
const SPARKLINE_HEIGHT = 22;
const BAR_GAP = 1;

export const AccountCallStatusSparkline = memo(function AccountCallStatusSparkline({
  status,
  highlightId,
  disabled,
  onOpen,
}: {
  status?: AccountCallStatusDTO;
  highlightId?: string;
  disabled?: boolean;
  onOpen?: () => void;
}) {
  const { t } = useTranslation();
  if (disabled || !status) {
    return <span className="block text-center text-[10px] leading-5 text-muted-foreground">—</span>;
  }
  const samples = status.samples;
  const interactive = Boolean(onOpen) && samples.length > 0;
  const summary = t("audits.callStatusSummary", {
    healthy: status.counts.healthy, soft: status.counts.soft, degraded: status.counts.degraded, failed: status.counts.failed,
  });
  const label = samples.length < 2
    ? `${t("audits.callStatusSparse")} · ${summary}`
    : summary;
  const open = () => { if (interactive) onOpen?.(); };
  const stop = (event: MouseEvent | KeyboardEvent) => event.stopPropagation();
  const content = (
    <span
      role={interactive ? "button" : "img"}
      tabIndex={interactive ? 0 : undefined}
      aria-label={interactive ? `${label}. ${t("audits.callStatusOpenHint")}` : label}
      className={cn(
        "inline-flex h-7 items-center justify-center rounded-md px-1 outline-none",
        interactive && "cursor-pointer hover:bg-muted/80 focus-visible:ring-2 focus-visible:ring-ring/50",
      )}
      onClick={stop}
      onDoubleClick={(event) => { stop(event); open(); }}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          stop(event);
          open();
        }
      }}
    >
      {samples.length === 0 ? (
        <span className="text-[10px] text-muted-foreground">—</span>
      ) : (
        <SparklineBars samples={samples} highlightId={highlightId} />
      )}
    </span>
  );
  return (
    <Tooltip>
      <TooltipTrigger asChild>{content}</TooltipTrigger>
      <TooltipContent className="max-w-72 text-left leading-5" side="top">
        <div>{label}</div>
        {interactive ? <div className="mt-1 text-primary-foreground/65">{t("audits.callStatusOpenHint")}</div> : null}
      </TooltipContent>
    </Tooltip>
  );
});

function SparklineBars({ samples, highlightId }: { samples: AccountCallStatusDTO["samples"]; highlightId?: string }) {
  const count = Math.max(samples.length, 1);
  const barWidth = Math.max(1, (SPARKLINE_WIDTH - BAR_GAP * (count - 1)) / count);
  return (
    <svg width={SPARKLINE_WIDTH} height={SPARKLINE_HEIGHT} viewBox={`0 0 ${SPARKLINE_WIDTH} ${SPARKLINE_HEIGHT}`} aria-hidden="true" className="overflow-visible">
      {samples.map((sample, index) => {
        const height = Math.max(2, SPARKLINE_HEIGHT * (CALL_LEVEL_HEIGHT[sample.l] ?? 0.28));
        const x = index * (barWidth + BAR_GAP);
        const highlighted = highlightId !== undefined && sample.id === highlightId;
        return (
          <rect
            key={sample.id}
            x={x}
            y={SPARKLINE_HEIGHT - height}
            width={barWidth}
            height={height}
            rx={0.6}
            className={cn(CALL_LEVEL_CLASS[sample.l] ?? "fill-muted-foreground/50", highlighted && "stroke-foreground stroke-[1.2]")}
          />
        );
      })}
    </svg>
  );
}
