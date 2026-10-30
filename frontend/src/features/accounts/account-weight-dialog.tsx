import { useMutation } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { setAccountWeight, type AccountDTO } from "@/features/accounts/accounts-api";

export function AccountWeightDialog({
  account,
  open,
  onOpenChange,
  onSaved,
}: {
  account: AccountDTO | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [value, setValue] = useState("");
  useEffect(() => {
    setValue(account ? String(account.weight) : "");
  }, [account, open]);
  const mutation = useMutation({
    mutationFn: (weight: number | null) => setAccountWeight(account?.id ?? "", weight),
    onSuccess: () => {
      toast.success(t("accounts.weightSaved"));
      onSaved();
      onOpenChange(false);
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("common.retry")),
  });
  const parsed = Number(value);
  const valid = value.trim() !== "" && Number.isInteger(parsed) && parsed >= 0 && parsed <= 100;

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!mutation.isPending) onOpenChange(next); }}>
      <DialogContent className="max-w-[380px]">
        <DialogHeader>
          <DialogTitle>{t("accounts.editWeightTitle")}</DialogTitle>
          <DialogDescription>{t("accounts.editWeightDescription")}</DialogDescription>
        </DialogHeader>
        <Input
          type="number"
          min={0}
          max={100}
          step={1}
          value={value}
          onChange={(event) => setValue(event.target.value)}
          aria-label={t("accounts.weight")}
        />
        <DialogFooter>
          <Button type="button" variant="secondary" disabled={mutation.isPending || !account?.weightManual} onClick={() => mutation.mutate(null)}>
            {t("accounts.restoreWeight")}
          </Button>
          <Button type="button" disabled={mutation.isPending || !valid} onClick={() => mutation.mutate(parsed)}>
            {t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
