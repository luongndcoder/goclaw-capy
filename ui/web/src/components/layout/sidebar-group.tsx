import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

interface SidebarGroupProps {
  label: string;
  collapsed?: boolean;
  children: React.ReactNode;
  // Collapsible section (icon-expanded sidebar only). When omitted the
  // group always renders its items, preserving previous behavior.
  groupKey?: string;
  open?: boolean;
  onToggle?: (group: string) => void;
}

export function SidebarGroup({ label, collapsed, children, groupKey, open = true, onToggle }: SidebarGroupProps) {
  const collapsible = groupKey != null && onToggle != null && !collapsed;
  if (!open && !collapsed) {
    return (
      <div className="space-y-1">
        <button
          type="button"
          aria-expanded={false}
          onClick={() => groupKey && onToggle?.(groupKey)}
          className="flex w-full items-center justify-between rounded-md px-3 py-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
        >
          <span className="truncate">{label}</span>
          <ChevronDown className="h-3.5 w-3.5 shrink-0 -rotate-90" />
        </button>
      </div>
    );
  }
  return (
    <div className="space-y-1">
      {!collapsed && collapsible ? (
        <button
          type="button"
          aria-expanded={open}
          onClick={() => groupKey && onToggle?.(groupKey)}
          className="flex w-full items-center justify-between rounded-md px-3 py-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
        >
          <span className="truncate">{label}</span>
          <ChevronDown className={cn("h-3.5 w-3.5 shrink-0 transition-transform", !open && "-rotate-90")} />
        </button>
      ) : (
        !collapsed && (
          <p className="px-3 py-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            {label}
          </p>
        )
      )}
      {collapsed && <div className="mx-auto my-1 h-px w-6 bg-border" />}
      <div className={cn("space-y-0.5", collapsed && "flex flex-col items-center")}>
        {children}
      </div>
    </div>
  );
}
