import type { ReactNode } from "react";
import { Table, type TableProps } from "antd";

export function PageHeader({
  kicker,
  title,
  description,
  actions,
}: {
  kicker?: string;
  title: string;
  description: string;
  actions?: ReactNode;
}) {
  return (
    <header className="v12-page-head">
      <div>
        {kicker ? <div className="v12-kicker">{kicker}</div> : null}
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      {actions ? <div className="v12-actions">{actions}</div> : null}
    </header>
  );
}

export function Surface({
  title,
  extra,
  children,
}: {
  title?: string;
  extra?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="v12-surface">
      {title || extra ? (
        <div className="v12-surface-head">
          {title ? <h2>{title}</h2> : <span />}
          {extra}
        </div>
      ) : null}
      {children}
    </section>
  );
}

export function Field({
  label,
  htmlFor,
  help,
  children,
}: {
  label: string;
  htmlFor?: string;
  help?: string;
  children: ReactNode;
}) {
  return (
    <div style={{ display: "grid", gap: 6 }}>
      <label htmlFor={htmlFor} style={{ color: "#505a70", fontSize: 12 }}>
        {label}
      </label>
      {children}
      {help ? (
        <span style={{ color: "#788196", fontSize: 11, lineHeight: 1.7 }}>
          {help}
        </span>
      ) : null}
    </div>
  );
}

export function Toolbar({ children }: { children: ReactNode }) {
  return <div className="v12-toolbar">{children}</div>;
}

const tones: Record<string, string> = {
  success: "success",
  healthy: "success",
  ready: "success",
  active: "success",
  fresh: "success",
  verified: "success",
  error: "danger",
  failed: "danger",
  conflicted: "danger",
  warning: "warning",
  partial: "warning",
  degraded: "warning",
  stale: "warning",
  unavailable: "warning",
  disabled: "warning",
  info: "info",
  running: "info",
};

export function StatusPill({
  value,
  label,
  tone,
}: {
  value?: string;
  label?: string;
  tone?: "success" | "warning" | "danger" | "info" | "neutral";
}) {
  const key = (value ?? "").toLowerCase();
  const resolved: "success" | "warning" | "danger" | "info" | "neutral" =
    tone ??
    (tones[key] as "success" | "warning" | "danger" | "info" | undefined) ??
    "neutral";
  return (
    <span className="v12-status" data-tone={resolved}>
      <span aria-hidden="true">●</span>
      {label ?? value ?? "未知"}
    </span>
  );
}

export function DataTable<T extends object>({ className, ...props }: TableProps<T>) {
  return (
    <Table<T>
      size="middle"
      {...props}
      className={["v12-table", className].filter(Boolean).join(" ")}
    />
  );
}

export function UnavailablePanel({
  title,
  message,
}: {
  title: string;
  message: string;
}) {
  return (
    <div className="v12-unavailable" role="status">
      <strong>{title}当前不可用</strong>
      <span>{message}</span>
    </div>
  );
}
