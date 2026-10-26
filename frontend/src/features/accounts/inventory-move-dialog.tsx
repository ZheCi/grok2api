import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { listInventoryGroups, stockAccounts, unstockAccounts } from "@/features/accounts/accounts-api";

export function InventoryMoveDialog({
  mode,
  ids,
  open,
  onOpenChange,
}: {
  mode: "stock" | "unstock";
  ids: string[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const [groupId, setGroupId] = useState("");
  const [groupName, setGroupName] = useState("");
  const [includeLinked, setIncludeLinked] = useState(false);
  const groupsQuery = useQuery({
    queryKey: ["inventory-groups"],
    queryFn: listInventoryGroups,
    enabled: open && mode === "stock",
  });
  const groups = groupsQuery.data?.items ?? [];
  const selectedGroup = groupId || groups.find((group) => group.name === "Default")?.id || groups[0]?.id || "";

  const mutation = useMutation({
    mutationFn: () => {
      if (mode === "unstock") return unstockAccounts({ ids, includeLinked });
      const name = groupName.trim();
      return stockAccounts({ ids, includeLinked, groupId: name ? undefined : selectedGroup, groupName: name || undefined });
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["accounts"] });
      await queryClient.invalidateQueries({ queryKey: ["inventory-groups"] });
      setIncludeLinked(false);
      setGroupName("");
      onOpenChange(false);
    },
    onError: (error: Error) => toast.error(error.message),
  });

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!mutation.isPending) onOpenChange(next); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{mode === "stock" ? "入库" : "移出库存"}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">{ids.length} 个账号</p>
          {mode === "stock" ? (
            <div className="space-y-2">
              <Label htmlFor="inventory-group">分组</Label>
              <select id="inventory-group" className="h-9 w-full rounded-md border bg-background px-3 text-sm" value={selectedGroup} onChange={(event) => setGroupId(event.target.value)}>
                {groups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}
              </select>
              <Input value={groupName} placeholder="或填写新分组名称" onChange={(event) => setGroupName(event.target.value)} />
            </div>
          ) : null}
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={includeLinked} onCheckedChange={(checked) => setIncludeLinked(checked === true)} />
            {mode === "stock" ? "同时将关联账号入库" : "同时移出关联账号"}
          </label>
        </div>
        <DialogFooter>
          <Button variant="secondary" onClick={() => onOpenChange(false)} disabled={mutation.isPending}>取消</Button>
          <Button onClick={() => mutation.mutate()} disabled={mutation.isPending || ids.length === 0}>{mode === "stock" ? "入库" : "移出"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
