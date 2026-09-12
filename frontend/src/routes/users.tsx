// T081 · 用户管理页（US6/FR-036）：仅 super_admin（router SuperAdminOnly；前端 can.manageUsers 守卫）。
// 创建/改角色/禁用/软删；角色变更与禁用即时吊销会话由服务端处理并记 permission_change 审计。
import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { usersApi } from "@/api/resources";
import type { User } from "@/api/types";
import { ApiError } from "@/api/client";
import { can, tokenStore } from "@/api/session";
import { fieldErrors, humanError } from "@/features/common";
import { Alert, Badge, Button, Card, DataTable, Field, Input, Modal, Select, Spinner, Td, Tr } from "@/components/ui";

const ROLES = ["super_admin", "gateway_admin", "developer", "viewer"] as const;
const ROLE_LABEL: Record<string, string> = {
  super_admin: "超级管理员",
  gateway_admin: "网关管理员",
  developer: "开发者",
  viewer: "观察者",
};
const ROLE_TONE: Record<string, "danger" | "info" | "success" | "neutral"> = {
  super_admin: "danger",
  gateway_admin: "info",
  developer: "success",
  viewer: "neutral",
};

const createSchema = z.object({
  username: z
    .string()
    .min(3, "用户名 ≥3 字符")
    .max(32, "用户名 ≤32 字符")
    .regex(/^[a-z0-9_.-]+$/, "仅小写字母/数字/_.-"),
  display_name: z.string().min(1, "显示名必填").max(64, "显示名 ≤64 字符"),
  password: z.string().min(12, "密码 ≥12 字符"),
  role: z.enum(ROLES),
});
type CreateValues = z.infer<typeof createSchema>;

const updateSchema = z.object({
  role: z.enum(ROLES),
  status: z.enum(["active", "disabled"]),
});
type UpdateValues = z.infer<typeof updateSchema>;

function UserCreateModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [banner, setBanner] = useState("");
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<CreateValues>({ resolver: zodResolver(createSchema), defaultValues: { role: "viewer" } });

  useEffect(() => {
    if (open) {
      reset({ username: "", display_name: "", password: "", role: "viewer" });
      setBanner("");
    }
  }, [open, reset]);

  const save = useMutation({
    mutationFn: (v: CreateValues) => usersApi.create(v),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["users"] });
      onClose();
    },
    onError: (e) => setBanner(humanError(e).text),
  });

  const srvErr = (e: unknown) => fieldErrors(e);

  return (
    <Modal open={open} onClose={onClose} title="新建用户">
      <form onSubmit={handleSubmit((v) => save.mutate(v))} className="space-y-4" aria-label="新建用户表单">
        <Field label="用户名" htmlFor="u-username" error={errors.username?.message ?? srvErr(save.error).username}>
          <Input id="u-username" autoCapitalize="off" {...register("username")} />
        </Field>
        <Field label="显示名" htmlFor="u-display" error={errors.display_name?.message ?? srvErr(save.error).display_name}>
          <Input id="u-display" {...register("display_name")} />
        </Field>
        <Field label="密码（≥12 字符）" htmlFor="u-pass" error={errors.password?.message ?? srvErr(save.error).password}>
          <Input id="u-pass" type="password" {...register("password")} />
        </Field>
        <Field label="角色" htmlFor="u-role" error={errors.role?.message}>
          <Select id="u-role" {...register("role")}>
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {ROLE_LABEL[r]}
              </option>
            ))}
          </Select>
        </Field>
        {banner && <Alert>{banner}</Alert>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" loading={save.isPending}>
            创建
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function UserEditModal({ user, onClose }: { user: User | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [banner, setBanner] = useState("");
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<UpdateValues>({ resolver: zodResolver(updateSchema) });

  useEffect(() => {
    if (user) {
      reset({ role: user.role as (typeof ROLES)[number], status: (user.status ?? "active") as "active" | "disabled" });
      setBanner("");
    }
  }, [user, reset]);

  const save = useMutation({
    mutationFn: (v: UpdateValues) =>
      usersApi.update(user!.id, { role: v.role, status: v.status, expected_version: user!.row_version ?? 0 }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["users"] });
      onClose();
    },
    onError: (e) => {
      if (e instanceof ApiError && e.code === "CONCURRENT_EDIT") {
        void qc.invalidateQueries({ queryKey: ["users"] });
      }
      setBanner(humanError(e).text);
    },
  });

  if (!user) return null;
  const isSelf = tokenStore.user()?.id === user.id;

  return (
    <Modal open={!!user} onClose={onClose} title={`编辑 · ${user.username}`}>
      <form onSubmit={handleSubmit((v) => save.mutate(v))} className="space-y-4" aria-label="编辑用户表单">
        <Field label="角色" htmlFor="e-role" error={errors.role?.message ?? fieldErrors(save.error).role}>
          <Select id="e-role" disabled={isSelf} {...register("role")}>
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {ROLE_LABEL[r]}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="状态" htmlFor="e-status" error={errors.status?.message ?? fieldErrors(save.error).status}>
          <Select id="e-status" disabled={isSelf} {...register("status")}>
            <option value="active">启用</option>
            <option value="disabled">禁用</option>
          </Select>
        </Field>
        {isSelf && <Alert>不可修改自己的角色或状态（避免自锁）。</Alert>}
        {banner && <Alert>{banner}</Alert>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" loading={save.isPending} disabled={isSelf}>
            保存
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function UserDeleteModal({ user, onClose }: { user: User | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [banner, setBanner] = useState("");
  const del = useMutation({
    mutationFn: () => usersApi.remove(user!.id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["users"] });
      onClose();
    },
    onError: (e) => setBanner(humanError(e).text),
  });
  if (!user) return null;
  const isSelf = tokenStore.user()?.id === user.id;
  return (
    <Modal open={!!user} onClose={onClose} title="删除用户">
      <p className="text-sm">
        确认删除用户「<b>{user.username}</b>」？该操作为软删除，将立即吊销其全部会话，历史审计记录保留。
      </p>
      {isSelf && <Alert className="mt-3">不可删除自己。</Alert>}
      {banner && <Alert className="mt-3">{banner}</Alert>}
      <div className="mt-4 flex justify-end gap-2">
        <Button variant="outline" onClick={onClose}>
          取消
        </Button>
        <Button variant="danger" loading={del.isPending} disabled={isSelf} onClick={() => del.mutate()}>
          删除
        </Button>
      </div>
    </Modal>
  );
}

export function UsersPage() {
  const canManage = can.manageUsers(tokenStore.user());
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<User | null>(null);
  const [deleting, setDeleting] = useState<User | null>(null);

  const { data, isPending, error } = useQuery({
    queryKey: ["users"],
    queryFn: () => usersApi.list({ page_size: 100 }),
  });

  if (!canManage) {
    return (
      <div className="space-y-4">
        <h1 className="text-xl font-semibold">用户管理</h1>
        <Alert>仅超级管理员可管理用户。</Alert>
      </div>
    );
  }

  const items = data?.items ?? [];

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">用户管理</h1>
          <p className="text-sm text-muted-foreground">账号、角色与启用状态；角色变更与禁用即时吊销会话并留审计。</p>
        </div>
        <Button onClick={() => setCreateOpen(true)}>新建用户</Button>
      </div>

      {isPending ? (
        <Spinner />
      ) : error ? (
        <Card className="border-destructive/40">{humanError(error).text}</Card>
      ) : (
        <DataTable head={["用户名", "显示名", "角色", "状态", "最后登录", "操作"]}>
          {items.map((u) => (
            <Tr key={u.id}>
              <Td className="font-mono text-xs">{u.username}</Td>
              <Td>{u.display_name}</Td>
              <Td>
                <Badge tone={ROLE_TONE[u.role] ?? "neutral"}>{ROLE_LABEL[u.role] ?? u.role}</Badge>
              </Td>
              <Td>
                <Badge tone={u.status === "disabled" ? "warning" : "success"}>
                  {u.status === "disabled" ? "已禁用" : "启用"}
                </Badge>
              </Td>
              <Td className="whitespace-nowrap text-xs text-muted-foreground">
                {u.last_login_at ? new Date(u.last_login_at).toLocaleString() : "—"}
              </Td>
              <Td>
                <div className="flex gap-2">
                  <Button size="sm" variant="outline" onClick={() => setEditing(u)}>
                    编辑
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setDeleting(u)}>
                    删除
                  </Button>
                </div>
              </Td>
            </Tr>
          ))}
          {items.length === 0 && (
            <Tr>
              <Td colSpan={6} className="py-8 text-center text-muted-foreground">
                暂无用户。
              </Td>
            </Tr>
          )}
        </DataTable>
      )}

      <UserCreateModal open={createOpen} onClose={() => setCreateOpen(false)} />
      <UserEditModal user={editing} onClose={() => setEditing(null)} />
      <UserDeleteModal user={deleting} onClose={() => setDeleting(null)} />
    </div>
  );
}
