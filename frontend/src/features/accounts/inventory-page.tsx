import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, MoreHorizontal, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableActionCell, TableActionHead, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { AccountQuota, ConsoleQuota, WebQuota } from "@/features/accounts/account-quota";
import { AccountNameCell } from "@/features/accounts/account-name-cell";
import { AccountStatus, AccountType, AccountTypeText, WebAccountType } from "@/features/accounts/accounts-page";
import { InventoryMoveDialog } from "@/features/accounts/inventory-move-dialog";
import {
  currentInventoryQualityTest,
  deleteAccounts,
  deleteInventoryGroup,
  detectBuildAccounts,
  listAccounts,
  listInventoryGroups,
  refreshAccountsQuota,
  startInventoryQualityTest,
  unstockAccounts,
  type AccountDTO,
  type AccountProvider,
  type InventoryQualityJobDTO,
} from "@/features/accounts/accounts-api";
import { getQualityGuardStatus, qualityGuardCallStatusThresholds } from "@/features/quality-guard/quality-guard-api";
import { listAllEgressNodes } from "@/features/settings/settings-api";
import { AccountCallStatusDialog } from "@/shared/components/account-call-status/account-call-status-dialog";
import { getAccountCallStatus, uniqueAccountIds } from "@/shared/components/account-call-status/account-call-status-api";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { PageHeader } from "@/shared/components/page-header";
import { Pagination } from "@/shared/components/pagination";
import { useDebouncedValue } from "@/shared/hooks/use-debounced-value";
import { formatDateTime } from "@/shared/lib/format";

const emptyJob: InventoryQualityJobDTO = { running: false, total: 0, done: 0, items: [] };
const batchSize = 2000;
const verdicts = ["ok", "degraded", "unknown"] as const;
type QualityVerdict = (typeof verdicts)[number];
type BulkAction = "detect" | "quota" | "delete";

function verdictOf(account: AccountDTO): QualityVerdict {
  if (account.qualityVerdict === "ok" || account.qualityVerdict === "degraded") return account.qualityVerdict;
  return "unknown";
}

function verdictLabel(value: AccountDTO["qualityVerdict"]): string {
  if (value === "ok") return "正常";
  if (value === "degraded") return "降智";
  return "未知";
}

function qualityStateLabel(state: string): string {
  switch (state) {
    case "ok": return "正常";
    case "degraded": return "降智";
    case "failed": return "失败";
    case "skipped": return "跳过";
    case "running": return "测试中";
    case "pending": return "等待";
    default: return state;
  }
}

async function loadInventoryAccounts(groupId: string, search: string): Promise<AccountDTO[]> {
  const base = { inventory: "stocked" as const, groupId: groupId || undefined, search, sortBy: "stockedAt", sortOrder: "desc" as const, pageSize: batchSize };
  const first = await listAccounts({ ...base, page: 1 });
  const loaded = [...first.items];
  for (let page = 2; page <= Math.ceil(first.total / batchSize); page += 1) {
    loaded.push(...(await listAccounts({ ...base, page })).items);
  }
  return loaded;
}

async function eachBatch(ids: string[], run: (ids: string[]) => Promise<void>) {
  for (let index = 0; index < ids.length; index += batchSize) await run(ids.slice(index, index + batchSize));
}

function toggleVerdict(current: Set<QualityVerdict>, value: QualityVerdict, checked: boolean) {
  const next = new Set(current);
  if (checked) next.add(value); else next.delete(value);
  return next;
}

function VerdictChecks({ selected, counts, ready, disabled, onToggle }: {
  selected: Set<QualityVerdict>;
  counts: Record<QualityVerdict, number>;
  ready: boolean;
  disabled: boolean;
  onToggle: (value: QualityVerdict, checked: boolean) => void;
}) {
  return (
    <div className="space-y-1.5">
      {([
        ["ok", "正常"],
        ["degraded", "降智"],
        ["unknown", "未知"],
      ] as const).map(([value, label]) => (
        <label key={value} className="flex cursor-pointer items-center gap-3 rounded-md bg-muted/40 px-3 py-2.5 text-sm">
          <Checkbox checked={selected.has(value)} disabled={disabled} onCheckedChange={(checked) => onToggle(value, checked === true)} />
          <span>{label}</span>
          <span className="ml-auto tabular-nums text-muted-foreground">{ready ? counts[value] : <Spinner className="size-3.5" />}</span>
        </label>
      ))}
    </div>
  );
}

async function runByProvider(accounts: AccountDTO[], run: (provider: AccountProvider, ids: string[]) => Promise<void>) {
  const grouped = new Map<AccountProvider, string[]>();
  for (const account of accounts) grouped.set(account.provider, [...(grouped.get(account.provider) ?? []), account.id]);
  for (const [provider, ids] of grouped) await eachBatch(ids, (part) => run(provider, part));
}

export function InventoryPage() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const [groupId, setGroupId] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const debouncedSearch = useDebouncedValue(search, 300);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [moveIds, setMoveIds] = useState<string[]>([]);
  const [moveOpen, setMoveOpen] = useState(false);
  const [unstockAllOpen, setUnstockAllOpen] = useState(false);
  const [deleteAllOpen, setDeleteAllOpen] = useState(false);
  const [deleteGroupOpen, setDeleteGroupOpen] = useState(false);
  const [unstockVerdicts, setUnstockVerdicts] = useState<Set<QualityVerdict>>(new Set());
  const [deleteVerdicts, setDeleteVerdicts] = useState<Set<QualityVerdict>>(new Set());
  const [includeLinked, setIncludeLinked] = useState(false);
  const [scopeAccounts, setScopeAccounts] = useState<AccountDTO[] | null>(null);
  const [confirmAction, setConfirmAction] = useState<BulkAction | null>(null);
  const [confirmAll, setConfirmAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const [qualityIds, setQualityIds] = useState<string[]>([]);
  const [qualityAll, setQualityAll] = useState(false);
  const [qualityOpen, setQualityOpen] = useState(false);
  const [nodeIds, setNodeIds] = useState<string[]>([]);
  const [callAccount, setCallAccount] = useState<{ id: string; label: string } | null>(null);
  const groupsQuery = useQuery({ queryKey: ["inventory-groups"], queryFn: listInventoryGroups });
  const groups = groupsQuery.data?.items ?? [];
  const activeGroup = groupId || groups.find((group) => group.name === "Default")?.id || "";
  const qualityGuardQuery = useQuery({ queryKey: ["quality-guard", "policy"], queryFn: () => getQualityGuardStatus(), staleTime: 15_000 });
  const thresholds = qualityGuardCallStatusThresholds(qualityGuardQuery.data);
  const accountsQuery = useQuery({
    queryKey: ["accounts", "inventory", activeGroup, debouncedSearch, page, pageSize, thresholds?.softTPS, thresholds?.hardTPS, thresholds?.minGenMs, thresholds?.failClosed],
    queryFn: () => listAccounts({ inventory: "stocked", groupId: activeGroup || undefined, search: debouncedSearch, page, pageSize, sortBy: "stockedAt", sortOrder: "desc", softTPS: thresholds?.softTPS, hardTPS: thresholds?.hardTPS, minGenMs: thresholds?.minGenMs, failClosed: thresholds?.failClosed }),
    enabled: groupsQuery.isSuccess,
    placeholderData: keepPreviousData,
  });
  const items = accountsQuery.data?.items ?? [];
  const activeGroupRecord = groups.find((group) => group.id === activeGroup);
  const groupName = activeGroupRecord?.name || "当前分组";
  const canDeleteGroup = Boolean(activeGroupRecord && activeGroupRecord.name !== "Default");
  const scopeLabel = debouncedSearch.trim() ? "当前搜索结果" : `分组「${groupName}」`;
  const accountIds = useMemo(() => uniqueAccountIds(items.map((account) => account.id)), [items]);
  const callStatusQuery = useQuery({
    queryKey: ["account-call-status", accountIds.join(","), thresholds?.softTPS, thresholds?.hardTPS, thresholds?.minGenMs, thresholds?.failClosed],
    queryFn: ({ signal }) => getAccountCallStatus({ accountIds, thresholds }, signal),
    enabled: accountIds.length > 0,
    staleTime: 15_000,
  });
  const nodesQuery = useQuery({ queryKey: ["egress-nodes", "inventory-test"], queryFn: () => listAllEgressNodes(), enabled: qualityOpen, staleTime: 60_000 });
  const qualityQuery = useQuery({ queryKey: ["inventory-quality"], queryFn: currentInventoryQualityTest, refetchInterval: (query) => query.state.data?.job.running ? 1000 : false });
  const job = qualityQuery.data?.job ?? emptyJob;
  const selectedAccounts = items.filter((account) => selected.has(account.id));

  useEffect(() => { setSelected(new Set()); }, [activeGroup, debouncedSearch, page, pageSize]);
  const sawQualityRun = useRef(false);
  useEffect(() => {
    if (job.running) {
      sawQualityRun.current = true;
      return;
    }
    if (!sawQualityRun.current) return;
    sawQualityRun.current = false;
    void queryClient.invalidateQueries({ queryKey: ["account-call-status"] });
    void queryClient.invalidateQueries({ queryKey: ["accounts", "inventory"] });
  }, [job.running, queryClient]);

  async function refreshAccounts() {
    await queryClient.invalidateQueries({ queryKey: ["accounts"] });
    await queryClient.invalidateQueries({ queryKey: ["account-call-status"] });
  }

  const quotaMutation = useMutation({
    mutationFn: (accounts: AccountDTO[]) => runByProvider(accounts, async (provider, ids) => { await refreshAccountsQuota(ids, provider); }),
    onSuccess: () => { setConfirmAction(null); toast.success("额度已更新"); void refreshAccounts(); },
    onError: (error: Error) => toast.error(error.message),
  });
  const detectMutation = useMutation({
    mutationFn: (accounts: AccountDTO[]) => eachBatch(accounts.filter((account) => account.provider === "grok_build").map((account) => account.id), async (ids) => { await detectBuildAccounts({ ids }); }),
    onSuccess: () => { setConfirmAction(null); toast.success("检测完成"); void refreshAccounts(); },
    onError: (error: Error) => toast.error(error.message),
  });
  const deleteMutation = useMutation({
    mutationFn: (accounts: AccountDTO[]) => runByProvider(accounts, async (provider, ids) => { await deleteAccounts(ids, provider); }),
    onSuccess: () => { setSelected(new Set()); setConfirmAction(null); setDeleteAllOpen(false); toast.success("已删除"); void refreshAccounts(); },
    onError: (error: Error) => toast.error(error.message),
  });
  const deleteGroupMutation = useMutation({
    mutationFn: (id: string) => deleteInventoryGroup(id),
    onSuccess: () => {
      setDeleteGroupOpen(false);
      setGroupId("");
      toast.success("分组已删除");
      void queryClient.invalidateQueries({ queryKey: ["inventory-groups"] });
    },
    onError: (error: Error) => toast.error(error.message),
  });
  const unstockMutation = useMutation({
    mutationFn: (input: { ids: string[]; includeLinked: boolean }) => eachBatch(input.ids, (part) => unstockAccounts({ ids: part, includeLinked: input.includeLinked }).then(() => undefined)),
    onSuccess: () => { setSelected(new Set()); setUnstockAllOpen(false); toast.success("已移出库存"); void refreshAccounts(); },
    onError: (error: Error) => toast.error(error.message),
  });
  const qualityMutation = useMutation({
    mutationFn: (input: { ids: string[]; nodeIds: string[] }) => startInventoryQualityTest(input),
    onSuccess: (result) => {
      if (!result.started) toast.error("已有一批在跑");
      void qualityQuery.refetch();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  useEffect(() => {
    if (!unstockAllOpen && !deleteAllOpen) return;
    let cancelled = false;
    setScopeAccounts(null);
    void loadInventoryAccounts(activeGroup, debouncedSearch).then((accounts) => {
      if (!cancelled) setScopeAccounts(accounts);
    }).catch((error: Error) => {
      if (!cancelled) toast.error(error.message);
    });
    return () => { cancelled = true; };
  }, [unstockAllOpen, deleteAllOpen, activeGroup, debouncedSearch]);

  function toggle(id: string, checked: boolean) {
    setSelected((current) => {
      const next = new Set(current);
      if (checked) next.add(id); else next.delete(id);
      return next;
    });
  }

  function openBulk(action: "unstock" | "quality" | BulkAction) {
    if (selected.size > 0) {
      if (action === "unstock") { setMoveIds([...selected]); setMoveOpen(true); return; }
      if (action === "quality") { setQualityAll(false); setQualityIds([...selected]); setQualityOpen(true); return; }
      setConfirmAll(false);
      setConfirmAction(action);
      return;
    }
    if (action === "unstock") {
      setUnstockVerdicts(new Set());
      setIncludeLinked(false);
      setUnstockAllOpen(true);
      return;
    }
    if (action === "quality") { setQualityAll(true); setQualityIds([]); setQualityOpen(true); return; }
    if (action === "delete") { setDeleteVerdicts(new Set()); setDeleteAllOpen(true); return; }
    setConfirmAll(true);
    setConfirmAction(action);
  }

  async function accountsForConfirm(): Promise<AccountDTO[]> {
    if (!confirmAll) return selectedAccounts;
    return loadInventoryAccounts(activeGroup, debouncedSearch);
  }

  async function runConfirm() {
    if (!confirmAction) return;
    setBusy(true);
    try {
      const accounts = await accountsForConfirm();
      if (confirmAction === "detect") {
        const build = accounts.filter((account) => account.provider === "grok_build");
        if (build.length === 0) { toast.error("没有可检测的 Grok Build 账号"); return; }
        detectMutation.mutate(build);
        return;
      }
      if (accounts.length === 0) { toast.error("没有可处理的账号"); return; }
      if (confirmAction === "quota") quotaMutation.mutate(accounts);
      else deleteMutation.mutate(accounts);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "操作失败");
    } finally {
      setBusy(false);
    }
  }

  async function beginQuality() {
    let ids = qualityIds;
    if (qualityAll) {
      setBusy(true);
      try {
        ids = (await loadInventoryAccounts(activeGroup, debouncedSearch)).map((account) => account.id);
        setQualityIds(ids);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "操作失败");
        setBusy(false);
        return;
      }
      setBusy(false);
    }
    if (ids.length === 0) { toast.error("没有可测试的账号"); return; }
    qualityMutation.mutate({ ids, nodeIds });
  }

  const verdictCounts = { ok: 0, degraded: 0, unknown: 0 };
  for (const account of scopeAccounts ?? []) verdictCounts[verdictOf(account)] += 1;
  const unstockIds = (scopeAccounts ?? []).filter((account) => unstockVerdicts.has(verdictOf(account))).map((account) => account.id);
  const deleteIds = (scopeAccounts ?? []).filter((account) => deleteVerdicts.has(verdictOf(account))).map((account) => account.id);
  const confirmPending = busy || detectMutation.isPending || quotaMutation.isPending || deleteMutation.isPending;
  const confirmTitle = confirmAction === "detect"
    ? (confirmAll ? "检测全部账号？" : `检测所选 ${selected.size} 个账号？`)
    : confirmAction === "quota"
      ? (confirmAll ? "更新全部账号的额度？" : `更新所选 ${selected.size} 个账号的额度？`)
      : (confirmAll ? "删除全部账号？" : `删除所选 ${selected.size} 个账号？`);
  const confirmDescription = confirmAction === "detect"
    ? `将对${confirmAll ? scopeLabel : `所选 ${selected.size} 个账号`}里的 Grok Build 账号发起一次检测。`
    : confirmAction === "quota"
      ? `将重新获取${confirmAll ? `${scopeLabel}里全部账号` : `所选 ${selected.size} 个账号`}的额度数据。`
      : `将删除${confirmAll ? `${scopeLabel}里的全部账号` : `所选 ${selected.size} 个账号`}。此操作无法撤销。`;

  return (
    <div className="space-y-4">
      <PageHeader title="库存" description="库存中的账号不参与调度。移出后按原来的启用和冷却规则回到账号页。" actions={
        <>
          <select className="h-9 rounded-md border bg-background px-3 text-sm" value={activeGroup} onChange={(event) => { setGroupId(event.target.value); setPage(1); }} aria-label="库存分组">
            {groups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}
          </select>
          <Button size="sm" variant="secondary" className="bg-destructive/10 text-destructive hover:bg-destructive/15 hover:text-destructive" disabled={!canDeleteGroup || deleteGroupMutation.isPending} title={activeGroupRecord?.name === "Default" ? "Default 分组不能删除" : undefined} onClick={() => setDeleteGroupOpen(true)}>删除分组</Button>
        </>
      } />
      <DataTableShell toolbar={(
        <div className="flex w-full flex-wrap items-center gap-2">
          <div className="relative min-w-0 flex-1 sm:w-64 sm:flex-none">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input className="h-8 pl-9 text-xs" value={search} onChange={(event) => { setSearch(event.target.value); setPage(1); }} placeholder={t("accounts.search")} aria-label={t("accounts.search")} />
          </div>
          <div className="ml-auto flex flex-wrap items-center justify-end gap-1.5">
            {selected.size > 0 ? <span className="mr-1 text-xs text-muted-foreground">已选 {selected.size}</span> : null}
            <Button size="sm" variant="secondary" disabled={!activeGroup || confirmPending} onClick={() => openBulk("unstock")}>移出库存</Button>
            <Button size="sm" variant="secondary" disabled={!activeGroup || confirmPending || (selected.size > 0 && selectedAccounts.every((account) => account.provider !== "grok_build"))} onClick={() => openBulk("detect")}>检测账号</Button>
            <Button size="sm" variant="secondary" disabled={!activeGroup || confirmPending} onClick={() => openBulk("quota")}>额度更新</Button>
            <Button size="sm" variant="secondary" disabled={!activeGroup || confirmPending} onClick={() => openBulk("quality")}>降智测试</Button>
            <Button size="sm" variant="secondary" className="bg-destructive/10 text-destructive hover:bg-destructive/15 hover:text-destructive" disabled={!activeGroup || confirmPending} onClick={() => openBulk("delete")}>删除</Button>
            {job.total > 0 ? <Button size="sm" variant="secondary" onClick={() => setQualityOpen(true)}>降智测试进度 {job.done}/{job.total}</Button> : null}
          </div>
        </div>
      )} footer={accountsQuery.data && accountsQuery.data.total > 0 ? <Pagination page={accountsQuery.data.page} pageSize={accountsQuery.data.pageSize} total={accountsQuery.data.total} onPageChange={setPage} onPageSizeChange={(value) => { setPageSize(value); setPage(1); }} /> : undefined}>
      <Table className="table-fixed min-w-[1080px]">
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="w-10 px-2"><Checkbox checked={items.length > 0 && selected.size === items.length} onCheckedChange={(checked) => setSelected(checked === true ? new Set(items.map((account) => account.id)) : new Set())} /></TableHead>
            <TableHead>账号</TableHead>
            <TableHead className="text-center">类型</TableHead>
            <TableHead className="text-center">状态</TableHead>
            <TableHead>额度</TableHead>
            <TableHead>凭据续期</TableHead>
            <TableHead>创建时间</TableHead>
            <TableHead>入库时间</TableHead>
            <TableHead>降智判定</TableHead>
            <TableActionHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((account) => (
            <TableRow key={account.id} className="group h-20 [&>td]:py-1.5" data-state={selected.has(account.id) ? "selected" : undefined}>
              <TableCell className="px-2"><Checkbox checked={selected.has(account.id)} onCheckedChange={(checked) => toggle(account.id, checked === true)} aria-label={account.name} /></TableCell>
              <TableCell className="min-w-0"><AccountNameCell account={account} /></TableCell>
              <TableCell className="text-center whitespace-nowrap">{account.provider === "grok_web" ? <WebAccountType tier={account.webTier} /> : account.provider === "grok_console" ? <AccountTypeText label={t("accountType.console")} variant="free" /> : <AccountType quota={account.quota} />}</TableCell>
              <TableCell className="text-center whitespace-nowrap">
                <AccountStatus account={account} callStatus={callStatusQuery.data?.accounts?.[account.id]} onOpenCallStatus={() => setCallAccount({ id: account.id, label: account.name || account.email || account.id })} />
              </TableCell>
              <TableCell>{account.provider === "grok_web" ? <WebQuota windows={account.quotaWindows ?? []} locale={i18n.language} tier={account.webTier} /> : account.provider === "grok_console" ? <ConsoleQuota windows={account.quotaWindows ?? []} locale={i18n.language} /> : <AccountQuota quota={account.quota} billing={account.billing} locale={i18n.language} />}</TableCell>
              <TableCell className="whitespace-nowrap pl-4 text-xs">
                {account.provider === "grok_build" ? (account.refreshable ? (
                  <Tooltip>
                    <TooltipTrigger asChild><span tabIndex={0} className="cursor-help font-medium text-emerald-700 dark:text-emerald-300">{t("accountCredential.autoRefresh")}</span></TooltipTrigger>
                    <TooltipContent>{account.expiresAt ? t("accountCredential.expiresAt", { time: formatDateTime(account.expiresAt, i18n.language) }) : t("accountCredential.expiryUnknown")}</TooltipContent>
                  </Tooltip>
                ) : <span className="font-medium text-amber-700 dark:text-amber-300">{t("accountCredential.noAutoRefresh")}</span>) : null}
              </TableCell>
              <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(account.createdAt, i18n.language)}</TableCell>
              <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{formatDateTime(account.stockedAt, i18n.language)}</TableCell>
              <TableCell className="whitespace-nowrap text-xs">{verdictLabel(account.qualityVerdict)}</TableCell>
              <TableActionCell>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild><Button variant="ghost" size="icon" className="size-8" aria-label={t("common.actions")}><MoreHorizontal /></Button></DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem onClick={() => { setMoveIds([account.id]); setMoveOpen(true); }}>移出库存</DropdownMenuItem>
                    {account.provider === "grok_build" ? <DropdownMenuItem disabled={detectMutation.isPending} onClick={() => detectMutation.mutate([account])}>检测账号</DropdownMenuItem> : null}
                    <DropdownMenuItem disabled={quotaMutation.isPending} onClick={() => quotaMutation.mutate([account])}>额度更新</DropdownMenuItem>
                    <DropdownMenuItem onClick={() => { setQualityAll(false); setQualityIds([account.id]); setQualityOpen(true); }}>降智测试</DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem className="text-destructive focus:text-destructive" disabled={deleteMutation.isPending} onClick={() => { if (window.confirm(`删除 ${account.name}？`)) deleteMutation.mutate([account]); }}>删除</DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </TableActionCell>
            </TableRow>
          ))}
          {items.length === 0 ? <TableRow><TableCell colSpan={10} className="text-center text-sm text-muted-foreground">这个分组里没有账号</TableCell></TableRow> : null}
        </TableBody>
      </Table>
      </DataTableShell>
      <InventoryMoveDialog mode="unstock" ids={moveIds} open={moveOpen} onOpenChange={setMoveOpen} />
      <Dialog open={unstockAllOpen} onOpenChange={(open) => { if (!unstockMutation.isPending) setUnstockAllOpen(open); }}>
        <DialogContent className="max-w-[440px]">
          <DialogHeader>
            <DialogTitle>移出库存</DialogTitle>
            <DialogDescription>未勾选具体账号。按降智判定移出{scopeLabel}里的账号。</DialogDescription>
          </DialogHeader>
          <VerdictChecks selected={unstockVerdicts} counts={verdictCounts} ready={scopeAccounts !== null} disabled={unstockMutation.isPending} onToggle={(value, checked) => setUnstockVerdicts((current) => toggleVerdict(current, value, checked))} />
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={includeLinked} disabled={unstockMutation.isPending} onCheckedChange={(checked) => setIncludeLinked(checked === true)} />
            同时移出关联账号
          </label>
          <DialogFooter>
            <Button variant="secondary" disabled={unstockMutation.isPending} onClick={() => setUnstockAllOpen(false)}>取消</Button>
            <Button disabled={unstockMutation.isPending || scopeAccounts === null || unstockIds.length === 0} onClick={() => unstockMutation.mutate({ ids: unstockIds, includeLinked })}>移出 {unstockIds.length || ""}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={deleteGroupOpen} onOpenChange={(open) => { if (!deleteGroupMutation.isPending) setDeleteGroupOpen(open); }}>
        <DialogContent className="max-w-[440px]">
          <DialogHeader>
            <DialogTitle>删除分组「{groupName}」？</DialogTitle>
            <DialogDescription>只删除空分组。组里还有账号时，先把账号移出库存。</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="secondary" disabled={deleteGroupMutation.isPending} onClick={() => setDeleteGroupOpen(false)}>取消</Button>
            <Button className="bg-destructive text-white hover:bg-destructive/90" disabled={deleteGroupMutation.isPending || !canDeleteGroup} onClick={() => { if (activeGroup) deleteGroupMutation.mutate(activeGroup); }}>
              {deleteGroupMutation.isPending ? <Spinner /> : null}
              删除分组
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={deleteAllOpen} onOpenChange={(open) => { if (!deleteMutation.isPending) setDeleteAllOpen(open); }}>
        <DialogContent className="max-w-[440px]">
          <DialogHeader>
            <DialogTitle>删除</DialogTitle>
            <DialogDescription>未勾选具体账号。按降智判定删除{scopeLabel}里的账号。此操作无法撤销。</DialogDescription>
          </DialogHeader>
          <VerdictChecks selected={deleteVerdicts} counts={verdictCounts} ready={scopeAccounts !== null} disabled={deleteMutation.isPending} onToggle={(value, checked) => setDeleteVerdicts((current) => toggleVerdict(current, value, checked))} />
          <DialogFooter>
            <Button variant="secondary" disabled={deleteMutation.isPending} onClick={() => setDeleteAllOpen(false)}>取消</Button>
            <Button className="bg-destructive text-white hover:bg-destructive/90" disabled={deleteMutation.isPending || scopeAccounts === null || deleteIds.length === 0} onClick={() => deleteMutation.mutate((scopeAccounts ?? []).filter((account) => deleteVerdicts.has(verdictOf(account))))}>删除 {deleteIds.length || ""}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={confirmAction !== null} onOpenChange={(open) => { if (!open && !confirmPending) setConfirmAction(null); }}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{confirmTitle}</DialogTitle>
            <DialogDescription>{confirmDescription}{confirmAll ? " 未勾选账号时默认处理全部。" : ""}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="secondary" disabled={confirmPending} onClick={() => setConfirmAction(null)}>取消</Button>
            <Button className={confirmAction === "delete" ? "bg-destructive text-white hover:bg-destructive/90" : undefined} disabled={confirmPending} onClick={() => void runConfirm()}>
              {confirmPending ? <Spinner /> : null}
              {confirmAction === "detect" ? "开始检测" : confirmAction === "quota" ? "更新额度" : "删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={qualityOpen} onOpenChange={setQualityOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>降智测试</DialogTitle>
            <DialogDescription>{qualityAll ? `未勾选账号，将测试${scopeLabel}里的全部账号。` : `将测试所选 ${qualityIds.length} 个账号。`}</DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5">
              <p className="text-sm font-medium">测试节点</p>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" className="h-9 w-full justify-between px-3 font-normal">
                    <span className="truncate">{nodeIds.length === 0 ? "使用各自绑定的节点" : (nodesQuery.data?.items ?? []).filter((node) => nodeIds.includes(node.id)).map((node) => node.name).join("、") || `已选 ${nodeIds.length} 个节点`}</span>
                    <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="w-[var(--radix-dropdown-menu-trigger-width)]">
                  {nodesQuery.isPending ? <DropdownMenuItem disabled>加载中</DropdownMenuItem> : null}
                  {(nodesQuery.data?.items ?? []).map((node) => (
                    <DropdownMenuCheckboxItem key={node.id} checked={nodeIds.includes(node.id)} onSelect={(event) => event.preventDefault()} onCheckedChange={(checked) => setNodeIds((current) => checked ? [...current, node.id] : current.filter((id) => id !== node.id))}>
                      {node.name}
                    </DropdownMenuCheckboxItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
              <p className="text-xs text-muted-foreground">不选时走账号原来绑定的节点。选了之后，每个账号从中随机取一个。</p>
            </div>
            {job.total > 0 ? (
              <div className="space-y-1.5">
                <div className="flex items-center justify-between text-sm">
                  <span className="font-medium">进度</span>
                  <span className="tabular-nums text-muted-foreground">{job.done}/{job.total}{job.running ? " 进行中" : " 已结束"}</span>
                </div>
                <div className="max-h-48 divide-y overflow-auto rounded-md border">
                  {job.items.map((item) => (
                    <div key={item.accountId} className="flex items-start justify-between gap-3 px-3 py-2 text-sm">
                      <div className="min-w-0">
                        <div className="truncate">{item.name || item.accountId}</div>
                        {item.detail ? <div className="truncate text-xs text-muted-foreground">{item.detail}</div> : null}
                      </div>
                      <span className="shrink-0 text-xs text-muted-foreground">{qualityStateLabel(item.state)}</span>
                    </div>
                  ))}
                </div>
              </div>
            ) : null}
          </div>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setQualityOpen(false)}>关闭</Button>
            <Button disabled={qualityMutation.isPending || busy || job.running || (!qualityAll && qualityIds.length === 0)} onClick={() => void beginQuality()}>开始</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {callAccount ? <AccountCallStatusDialog accountId={callAccount.id} accountLabel={callAccount.label} open onOpenChange={(open) => { if (!open) setCallAccount(null); }} /> : null}
    </div>
  );
}
