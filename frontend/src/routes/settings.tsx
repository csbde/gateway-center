// T069 · 设置页：平台设置（super_admin）+ DNS 凭证管理（developer+ 写）。
import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { settingsApi } from "@/api/resources";
import { ApiError } from "@/api/client";
import { can, tokenStore } from "@/api/session";
import { Alert, Button, Card, Field, Input, Select, Spinner } from "@/components/ui";
import { humanError } from "@/features/common";
import { CredentialsPage } from "@/features/credentials";

const settingsSchema = z.object({
  probe_interval_sec: z.coerce.number().int().min(5, "探测周期 ≥5 秒").max(3600, "探测周期 ≤3600 秒"),
  offline_threshold: z.coerce.number().int().min(1, "离线阈值 ≥1").max(10, "离线阈值 ≤10"),
  expiry_warn_days: z.coerce.number().int().min(1, "预警天数 ≥1").max(365, "预警天数 ≤365"),
  timezone: z.string().min(1, "时区必填"),
});

type SettingsValues = z.infer<typeof settingsSchema>;

const TIMEZONES = ["Asia/Shanghai", "UTC", "Asia/Tokyo", "Asia/Singapore", "Europe/London", "America/New_York"];

function PlatformSettingsForm() {
  const qc = useQueryClient();
  const canEdit = can.editSettings(tokenStore.user());
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState("");

  const { data, isPending, error } = useQuery({ queryKey: ["settings"], queryFn: () => settingsApi.get() });

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<SettingsValues>({ resolver: zodResolver(settingsSchema) });

  // 首次加载回填（仅 data 变化时）
  useEffect(() => {
    if (data) reset(data as SettingsValues);
  }, [data, reset]);

  const save = useMutation({
    mutationFn: (v: SettingsValues) =>
      settingsApi.update({ ...v, expected_version: data?.row_version ?? 0 }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["settings"] }),
    onError: (e) => {
      if (e instanceof ApiError) {
        setServerErr(
          Object.fromEntries(
            (e.details ?? []).filter((d) => d.field).map((d) => [d.field!, [d.message, d.hint].filter(Boolean).join("；")]),
          ),
        );
      }
      setBanner(humanError(e).text);
    },
  });

  if (isPending) return <Spinner />;
  if (error) return <Alert>{humanError(error).text}</Alert>;

  return (
    <Card className="space-y-4">
      <div className="text-sm font-medium">平台设置</div>
      {!canEdit && <Alert>仅超级管理员可修改平台设置。</Alert>}
      <form onSubmit={handleSubmit((v) => save.mutate(v))} className="space-y-4" aria-label="平台设置表单">
        <div className="grid grid-cols-2 gap-4">
          <Field label="探测周期（秒）" htmlFor="s-probe" error={errors.probe_interval_sec?.message ?? serverErr.probe_interval_sec}>
            <Input id="s-probe" type="number" disabled={!canEdit} {...register("probe_interval_sec")} />
          </Field>
          <Field label="离线阈值（连续失败次数）" htmlFor="s-offline" error={errors.offline_threshold?.message ?? serverErr.offline_threshold}>
            <Input id="s-offline" type="number" disabled={!canEdit} {...register("offline_threshold")} />
          </Field>
          <Field label="证书到期预警（天）" htmlFor="s-warn" error={errors.expiry_warn_days?.message ?? serverErr.expiry_warn_days}>
            <Input id="s-warn" type="number" disabled={!canEdit} {...register("expiry_warn_days")} />
          </Field>
          <Field label="时区" htmlFor="s-tz" error={errors.timezone?.message ?? serverErr.timezone}>
            <Select id="s-tz" disabled={!canEdit} {...register("timezone")}>
              {TIMEZONES.map((tz) => (
                <option key={tz} value={tz}>
                  {tz}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        {banner && <Alert>{banner}</Alert>}
        {canEdit && (
          <div className="flex justify-end">
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "保存中…" : "保存设置"}
            </Button>
          </div>
        )}
      </form>
    </Card>
  );
}

export function SettingsPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold">设置</h1>
        <p className="text-sm text-muted-foreground">平台运行参数与 DNS 凭证管理。</p>
      </div>
      <PlatformSettingsForm />
      <CredentialsPage />
    </div>
  );
}
