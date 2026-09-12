// T042 · 登录页（US1）：RHF + zod，会话写入与路由守卫在 routeTree.tsx/session.ts。
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useNavigate } from "@tanstack/react-router";
import { login } from "@/api/session";

const schema = z.object({
  username: z.string().min(1, "请输入用户名"),
  password: z.string().min(1, "请输入密码"),
});
type FormValues = z.infer<typeof schema>;

export function LoginPage() {
  const navigate = useNavigate();
  const [error, setError] = useState("");
  const {
    register,
    handleSubmit,
    formState: { isSubmitting },
  } = useForm<FormValues>({ resolver: zodResolver(schema) });

  const onSubmit = handleSubmit(async (values) => {
    setError("");
    try {
      await login(values.username, values.password);
      void navigate({ to: "/" });
    } catch (e) {
      setError(e instanceof Error ? e.message : "登录失败");
    }
  });

  return (
    <main className="flex min-h-screen items-center justify-center p-6">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm rounded-lg border bg-card p-6 shadow-sm"
        aria-label="登录"
      >
        <h1 className="text-lg font-semibold">Gateway Center</h1>
        <p className="mt-1 text-sm text-muted-foreground">统一网关管理中心</p>

        <label className="mt-5 block text-sm font-medium" htmlFor="username">
          用户名
        </label>
        <input
          id="username"
          autoComplete="username"
          className="mt-1 w-full rounded-md border bg-background px-3 py-2"
          {...register("username")}
        />

        <label className="mt-4 block text-sm font-medium" htmlFor="password">
          密码
        </label>
        <input
          id="password"
          type="password"
          autoComplete="current-password"
          className="mt-1 w-full rounded-md border bg-background px-3 py-2"
          {...register("password")}
        />

        {error && (
          <p role="alert" className="mt-3 text-sm text-destructive">
            {error}
          </p>
        )}

        <button
          type="submit"
          disabled={isSubmitting}
          className="mt-5 w-full rounded-md bg-primary px-4 py-2 font-medium text-primary-foreground disabled:opacity-60"
        >
          {isSubmitting ? "登录中…" : "登录"}
        </button>
      </form>
    </main>
  );
}
