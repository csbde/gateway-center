// T043–T047 共享 UI 原语（shadcn 思路：radix + cva + Tailwind 令牌；一期仅实装所需子集）。
import * as Dialog from "@radix-ui/react-dialog";
import { cva, type VariantProps } from "class-variance-authority";
import { Loader2, X } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";

const cx = (...cls: (string | false | null | undefined)[]) => cls.filter(Boolean).join(" ");

const buttonVariants = cva(
  "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        outline: "border bg-card hover:bg-muted",
        ghost: "hover:bg-muted",
        danger: "bg-destructive text-destructive-foreground hover:bg-destructive/90",
      },
      size: { default: "", sm: "px-2 py-1 text-xs" },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

export function Button({
  variant,
  size,
  className,
  loading,
  children,
  ...rest
}: ComponentProps<"button"> & VariantProps<typeof buttonVariants> & { loading?: boolean }) {
  return (
    <button type="button" className={cx(buttonVariants({ variant, size }), className)} disabled={loading || rest.disabled} {...rest}>
      {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />}
      {children}
    </button>
  );
}

export function Input({ className, ...rest }: ComponentProps<"input">) {
  return (
    <input
      className={cx(
        "w-full rounded-md border border-input bg-background px-3 py-2 text-sm",
        "aria-[invalid=true]:border-destructive",
        className,
      )}
      {...rest}
    />
  );
}

export function Select({ className, children, ...rest }: ComponentProps<"select">) {
  return (
    <select className={cx("w-full rounded-md border border-input bg-background px-3 py-2 text-sm", className)} {...rest}>
      {children}
    </select>
  );
}

export function Field({
  label,
  htmlFor,
  hint,
  error,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <div>
      <label className="block text-sm font-medium" htmlFor={htmlFor}>
        {label}
      </label>
      <div className="mt-1">{children}</div>
      {hint && !error && <p className="mt-1 text-xs text-muted-foreground">{hint}</p>}
      {error && (
        <p className="mt-1 text-xs text-destructive" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

const badgeVariants = cva("inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium", {
  variants: {
    tone: {
      neutral: "bg-muted text-muted-foreground",
      success: "bg-success/15 text-success",
      warning: "bg-warning/20 text-warning",
      danger: "bg-destructive/15 text-destructive",
      info: "bg-primary/10 text-primary",
    },
  },
  defaultVariants: { tone: "neutral" },
});

export type BadgeTone = "neutral" | "success" | "warning" | "danger" | "info";

export function Badge({ tone = "neutral", className, children }: { tone?: BadgeTone; className?: string; children: ReactNode }) {
  return <span className={cx(badgeVariants({ tone }), className)}>{children}</span>;
}

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cx("rounded-lg border bg-card p-4 shadow-sm", className)}>{children}</div>;
}

export function Alert({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      role="alert"
      className={cx("rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive", className)}
    >
      {children}
    </div>
  );
}

export function Modal({
  open,
  onClose,
  title,
  children,
  wide,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={(o) => !o && onClose()}>
      {/* container 显式指向 document.body：Portal 默认容器与表单所在 Document 不一致时，
          React Hook Form 的 ref 收集失效（handleSubmit 拿到 undefined）。 */}
      <Dialog.Portal container={document.body}>
        <Dialog.Overlay className="fixed inset-0 bg-black/40" />
        <Dialog.Content
          className={cx(
            "fixed left-1/2 top-1/2 max-h-[85vh] w-full -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-lg border bg-card p-5 shadow-lg",
            wide ? "max-w-2xl" : "max-w-md",
          )}
        >
          <div className="flex items-center justify-between">
            <Dialog.Title className="text-base font-semibold">{title}</Dialog.Title>
            <Dialog.Close asChild>
              <button type="button" aria-label="关闭" className="rounded-md p-1 hover:bg-muted">
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>
          <div className="mt-4">{children}</div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
      <Loader2 className="h-4 w-4 animate-spin" aria-hidden /> {label ?? "加载中…"}
    </div>
  );
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <div className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">{children}</div>;
}

/** 表格骨架：列表页统一样式（宽内容横向滚动，页面体不横滚）。 */
export function DataTable({ head, children }: { head: ReactNode[]; children: ReactNode }) {
  return (
    <div className="overflow-x-auto rounded-lg border bg-card">
      <table className="w-full text-left text-sm">
        <thead className="border-b bg-muted/50 text-xs uppercase text-muted-foreground">
          <tr>
            {head.map((h, i) => (
              <th key={i} className="px-3 py-2 font-medium">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

export function Tr({ children, className, onClick }: { children: ReactNode; className?: string; onClick?: () => void }) {
  return <tr onClick={onClick} className={cx("border-b last:border-0", className)}>{children}</tr>;
}

export function Td({ children, className, colSpan }: { children: ReactNode; className?: string; colSpan?: number }) {
  return (
    <td colSpan={colSpan} className={cx("px-3 py-2", className)}>
      {children}
    </td>
  );
}

export { cx };
