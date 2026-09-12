// T005 · 占位页统一组件（各故事页实装前的可导航骨架）。
export function Placeholder({ title }: { title: string }) {
  return (
    <div className="p-6">
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="mt-2 text-muted-foreground">该页面将在对应迭代实装。</p>
    </div>
  );
}
