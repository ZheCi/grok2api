import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Compass, Gauge, MoreHorizontal, Power, PowerOff, RefreshCw, RotateCw, SquareTerminal, TimerOff, Webhook } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { AccountWeightDialog } from "@/features/accounts/account-weight-dialog";
import { AccountQuota, ConsoleQuota, WebQuota } from "@/features/accounts/account-quota";
import { clearAccountCooldown, listAccounts, refreshAccountBilling, refreshAccountQuota, refreshAccountToken, updateAccountsEnabled, type AccountDTO, type AccountProvider } from "@/features/accounts/accounts-api";
import { RequestAuditDetailDialog } from "@/features/audits/request-audit-detail-dialog";
import { getRequestAudit, type AuditDTO } from "@/features/audits/request-audits-api";
import { getAccountCallStatusDetail, type CallStatusThresholdInput, type CallStatusWindow } from "@/shared/components/account-call-status/account-call-status-api";
import { CallStatusCharts } from "@/shared/components/account-call-status/call-status-charts";
import { EmptyState, ErrorState, LoadingState } from "@/shared/components/data-state";
import { cn } from "@/shared/lib/cn";

export function AccountCallStatusDialog({
  accountId,
  accountLabel,
  highlightId,
  open,
  onOpenChange,
  thresholds,
}: {
  accountId?: string;
  accountLabel?: string;
  highlightId?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  thresholds?: CallStatusThresholdInput;
}) {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const [window, setWindow] = useState<CallStatusWindow>("24h");
  const [includeProbes, setIncludeProbes] = useState(false);
  const [selectedAudit, setSelectedAudit] = useState<AuditDTO | null>(null);
  useEffect(() => {
    if (!open) {
      setSelectedAudit(null);
      return;
    }
    setWindow("24h");
    setIncludeProbes(false);
  }, [accountId, open]);
  const query = useQuery({
    queryKey: ["account-call-status-detail", accountId, window, includeProbes, thresholds?.softTPS, thresholds?.hardTPS, thresholds?.minGenMs, thresholds?.failClosed],
    queryFn: ({ signal }) => getAccountCallStatusDetail({
      accountId: accountId ?? "", window, includeProbes, thresholds,
    }, signal),
    enabled: open && Boolean(accountId),
  });
  const accountQuery = useQuery({
    queryKey: ["account-call-status-account", accountId],
    queryFn: async () => {
      const page = await listAccounts({ page: 1, pageSize: 20, search: `#${accountId}` });
      return page.items.find((item) => item.id === accountId) ?? null;
    },
    enabled: open && Boolean(accountId),
    staleTime: 30_000,
  });
  const data = query.data;
  const account = accountQuery.data;
  const fallbackLabel = accountLabel || (accountId ? `#${accountId}` : t("audits.callStatusTitle"));

  async function openAudit(auditId: string): Promise<void> {
    try {
      const detail = await getRequestAudit(auditId);
      setSelectedAudit(detail.audit);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("common.retry"));
    }
  }

  return (
    <>
      <Dialog open={open} onOpenChange={(next) => { if (!next && selectedAudit) return; onOpenChange(next); }}>
        <DialogContent
          className="flex h-[min(760px,calc(100svh-2rem))] max-h-[calc(100svh-2rem)] min-h-0 flex-col gap-0 overflow-hidden p-0 text-xs sm:max-w-[960px]"
          onPointerDownOutside={(event) => { if (selectedAudit) event.preventDefault(); }}
          onInteractOutside={(event) => { if (selectedAudit) event.preventDefault(); }}
          onEscapeKeyDown={(event) => { if (selectedAudit) event.preventDefault(); }}
        >
          <DialogHeader className="shrink-0 px-5 pb-3 pt-4 pr-12">
            <DialogTitle>{t("audits.callStatusTitle")}</DialogTitle>
            <DialogDescription className="sr-only">{fallbackLabel}</DialogDescription>
          </DialogHeader>
          <div className="shrink-0 border-t px-5 py-3">
            {account ? (
              <AccountHeader
                account={account}
                locale={i18n.language}
                busy={accountQuery.isFetching}
                onChanged={() => {
                  void queryClient.invalidateQueries({ queryKey: ["account-call-status-account", accountId] });
                  void queryClient.invalidateQueries({ queryKey: ["accounts"] });
                  void queryClient.invalidateQueries({ queryKey: ["quality-guard-degrade-accounts"] });
                }}
              />
            ) : (
              <div className="text-[11px] text-muted-foreground">{accountQuery.isFetching ? t("common.loading") : fallbackLabel}</div>
            )}
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-3 border-t px-5 py-3">
            <Select value={window} onValueChange={(value) => setWindow(value as CallStatusWindow)}>
              <SelectTrigger className="h-8 w-auto min-w-28"><SelectValue /></SelectTrigger>
              <SelectContent>
                {(["1h", "6h", "24h", "7d"] as const).map((item) => (
                  <SelectItem key={item} value={item}>{t(`qualityGuard.degrade.windows.${item}`)}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <label className="flex items-center gap-2 text-[11px] text-muted-foreground">
              <Switch checked={includeProbes} onCheckedChange={setIncludeProbes} />
              {t("audits.callStatusIncludeProbes")}
            </label>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-5">
            {query.isError ? <ErrorState message={query.error.message} onRetry={() => void query.refetch()} /> : null}
            {query.isPending ? <LoadingState /> : null}
            {data?.truncated ? <p className="mb-2 text-[11px] text-muted-foreground">{t("audits.callStatusTruncated", { count: data.points.length })}</p> : null}
            {data && data.points.length === 0 ? <EmptyState message={t("audits.callStatusEmpty")} /> : null}
            {data && data.points.length > 0 ? (
              <CallStatusCharts
                points={data.points}
                totals={data.totals}
                thresholds={data.thresholds}
                locale={i18n.language}
                highlightId={highlightId}
                onOpenAudit={openAudit}
              />
            ) : null}
          </div>
        </DialogContent>
      </Dialog>
      <RequestAuditDetailDialog
        key={selectedAudit?.id ?? "closed"}
        audit={selectedAudit}
        open={selectedAudit !== null}
        onOpenChange={(next) => { if (!next) setSelectedAudit(null); }}
      />
    </>
  );
}

const providerIcon: Record<AccountProvider, { icon: typeof SquareTerminal; className: string }> = {
  grok_build: { icon: SquareTerminal, className: "text-quota-product-1" },
  grok_web: { icon: Compass, className: "text-quota-product-2" },
  grok_console: { icon: Webhook, className: "text-quota-product-4" },
};

function AccountHeader({ account, locale, busy, onChanged }: { account: AccountDTO; locale: string; busy?: boolean; onChanged: () => void }) {
  const { t } = useTranslation();
  const [weightOpen, setWeightOpen] = useState(false);
  const cooling = Boolean(account.cooldownUntil && new Date(account.cooldownUntil) > new Date());
  const identity = account.email?.trim() || account.name;
  const links = [
    account.provider,
    ...(account.linkedAccounts ?? []).map((linked) => linked.provider).filter((provider) => provider !== account.provider),
  ];
  const enableMutation = useMutation({
    mutationFn: (enabled: boolean) => updateAccountsEnabled([account.id], enabled, account.provider),
    onSuccess: (_, enabled) => {
      onChanged();
      toast.success(enabled ? t("accounts.updated") : t("accounts.statusDisabled"));
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("common.retry")),
  });
  const cooldownMutation = useMutation({
    mutationFn: () => clearAccountCooldown(account.id),
    onSuccess: () => {
      onChanged();
      toast.success(t("accounts.cooldownCleared"));
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("common.retry")),
  });
  const quotaMutation = useMutation({
    mutationFn: () => account.provider === "grok_build" ? refreshAccountBilling(account.id).then(() => account) : refreshAccountQuota(account.id),
    onSuccess: () => {
      onChanged();
      toast.success(t("accounts.billingRefreshed"));
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("common.retry")),
  });
  const tokenMutation = useMutation({
    mutationFn: () => refreshAccountToken(account.id),
    onSuccess: () => {
      onChanged();
      toast.success(t("accounts.authRefreshed"));
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("common.retry")),
  });
  const pending = busy || enableMutation.isPending || cooldownMutation.isPending || quotaMutation.isPending || tokenMutation.isPending;

  return (
    <div className="flex min-w-0 items-center gap-3 overflow-hidden text-[11px]">
      <div className="flex min-w-0 shrink-0 items-center gap-1.5">
        <span className="flex shrink-0 items-center gap-1">
          {links.map((provider) => {
            const { icon: Icon, className } = providerIcon[provider];
            return <Icon key={provider} className={cn("size-3.5", className)} />;
          })}
        </span>
        <span className="max-w-52 truncate font-medium text-foreground">{identity}</span>
      </div>
      <span className="shrink-0"><AccountTypeBadge account={account} /></span>
      <span className="shrink-0"><AccountStatusBadge account={account} /></span>
      <div className="min-w-0 flex-1">
        {account.provider === "grok_web"
          ? <WebQuota windows={account.quotaWindows ?? []} locale={locale} tier={account.webTier} />
          : account.provider === "grok_console"
            ? <ConsoleQuota windows={account.quotaWindows ?? []} locale={locale} />
            : <AccountQuota quota={account.quota} billing={account.billing} locale={locale} />}
      </div>
      <span className="shrink-0 tabular-nums text-[11px] text-muted-foreground">{t("accounts.weight")} {account.weight}</span>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button type="button" variant="ghost" size="icon" className="size-8 shrink-0" disabled={pending} aria-label={t("common.actions")}>
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="z-[70]">
          <DropdownMenuItem disabled={pending} onClick={() => setWeightOpen(true)}>
            <Gauge />{t("accounts.editWeight")}
          </DropdownMenuItem>
          {cooling ? (
            <DropdownMenuItem disabled={pending} onClick={() => cooldownMutation.mutate()}>
              <TimerOff />{t("accounts.clearCooldown")}
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem disabled={pending} onClick={() => enableMutation.mutate(!account.enabled)}>
            {account.enabled ? <PowerOff /> : <Power />}
            {account.enabled ? t("common.disable") : t("common.enable")}
          </DropdownMenuItem>
          <DropdownMenuItem disabled={pending} onClick={() => quotaMutation.mutate()}>
            <RefreshCw />{account.provider === "grok_build" ? t("accounts.refreshBilling") : t("accounts.refreshModeQuota")}
          </DropdownMenuItem>
          {account.provider === "grok_build" ? (
            <DropdownMenuItem disabled={pending} onClick={() => tokenMutation.mutate()}>
              <RotateCw />{t("accounts.refreshToken")}
            </DropdownMenuItem>
          ) : null}
        </DropdownMenuContent>
      </DropdownMenu>
      <AccountWeightDialog account={account} open={weightOpen} onOpenChange={setWeightOpen} onSaved={onChanged} />
    </div>
  );
}

function AccountTypeBadge({ account }: { account: AccountDTO }) {
  const { t } = useTranslation();
  if (account.provider === "grok_web") {
    const label = account.webTier === "basic" ? t("accountType.free") : account.webTier === "super" ? t("accountType.super") : account.webTier === "heavy" ? t("accountType.heavy") : t("accountType.auto");
    return <span className={cn("text-xs font-medium", account.webTier === "basic" ? "text-emerald-700 dark:text-emerald-300" : "text-primary")}>{label}</span>;
  }
  if (account.provider === "grok_console") {
    return <span className="text-xs font-medium text-emerald-700 dark:text-emerald-300">{t("accountType.console")}</span>;
  }
  if (account.quota.type === "unknown") {
    return <span className="text-xs text-muted-foreground">{t("accountType.pending")}</span>;
  }
  const isFree = account.quota.type === "free";
  return <span className={cn("text-xs font-medium", isFree ? "text-emerald-700 dark:text-emerald-300" : "text-primary")}>{isFree ? t("accountType.free") : t("accountType.paid")}</span>;
}

function AccountStatusBadge({ account }: { account: AccountDTO }) {
  const { t } = useTranslation();
  if (!account.enabled) return <Badge variant="outline" className="text-muted-foreground">{t("accounts.statusDisabled")}</Badge>;
  if (account.authStatus === "reauthRequired") return <Badge variant="destructive">{t("accounts.statusReauthRequired")}</Badge>;
  if (account.quota.status === "waitingReset") return <Badge variant="secondary" className="bg-amber-500/10 text-amber-700 dark:text-amber-300">{t("accounts.waitingReset")}</Badge>;
  if (account.quota.status === "probing") return <Badge variant="secondary" className="bg-sky-500/10 text-sky-700 dark:text-sky-300">{t("accounts.probing")}</Badge>;
  if (account.cooldownUntil && new Date(account.cooldownUntil) > new Date()) {
    return <Badge variant="secondary" className="bg-amber-500/10 text-amber-700 dark:text-amber-300">{t("accounts.statusCooldown")}</Badge>;
  }
  return <Badge variant="secondary" className="bg-emerald-500/10 text-emerald-700 dark:text-emerald-300">{t("accounts.statusActive")}</Badge>;
}


