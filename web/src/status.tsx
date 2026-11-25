import {
  IconAlertTriangle,
  IconBan,
  IconCalendar,
  IconCheck,
  IconCircleDashed,
  IconClock,
  IconHandClick,
  IconHelpCircle,
  IconLoader2,
  IconPlayerSkipForward,
  IconX,
  type Icon,
} from "@tabler/icons-react";

type Tone = "success" | "warning" | "failed" | "running" | "pending" | "neutral" | "muted";

type StatusInfo = { tone: Tone; icon: Icon; label: string; active: boolean };

const map: Record<string, StatusInfo> = {
  success: { tone: "success", icon: IconCheck, label: "passed", active: false },
  "success-with-warnings": { tone: "warning", icon: IconAlertTriangle, label: "warning", active: false },
  failed: { tone: "failed", icon: IconX, label: "failed", active: false },
  running: { tone: "running", icon: IconLoader2, label: "running", active: true },
  canceling: { tone: "neutral", icon: IconBan, label: "canceling", active: true },
  pending: { tone: "pending", icon: IconClock, label: "pending", active: true },
  preparing: { tone: "pending", icon: IconClock, label: "preparing", active: true },
  "waiting-for-resource": { tone: "pending", icon: IconClock, label: "waiting", active: true },
  waiting_for_resource: { tone: "pending", icon: IconClock, label: "waiting", active: true },
  "waiting-for-callback": { tone: "pending", icon: IconClock, label: "waiting", active: true },
  created: { tone: "neutral", icon: IconCircleDashed, label: "created", active: true },
  scheduled: { tone: "neutral", icon: IconCalendar, label: "scheduled", active: true },
  manual: { tone: "neutral", icon: IconHandClick, label: "manual", active: false },
  canceled: { tone: "neutral", icon: IconBan, label: "canceled", active: false },
  skipped: { tone: "muted", icon: IconPlayerSkipForward, label: "skipped", active: false },
};

const unknown: StatusInfo = { tone: "muted", icon: IconHelpCircle, label: "unknown", active: false };

export function statusInfo(status: string): StatusInfo {
  return map[status] ?? unknown;
}

export function isActive(status: string) {
  return statusInfo(status).active;
}

export function StatusBadge({ status, label }: { status: string; label?: string }) {
  const s = statusInfo(status);
  const I = s.icon;
  return (
    <span className={`badge tone-${s.tone}`} title={label || s.label}>
      <I size={14} stroke={2.4} className={status === "running" ? "spin" : undefined} aria-hidden />
      {s.label}
    </span>
  );
}

export function toneOf(status: string) {
  return statusInfo(status).tone;
}

export const statusFilterOptions: { value: string; label: string }[] = [
  { value: "", label: "Any status" },
  { value: "running", label: "Running" },
  { value: "pending", label: "Pending" },
  { value: "success", label: "Passed" },
  { value: "failed", label: "Failed" },
  { value: "canceled", label: "Canceled" },
  { value: "manual", label: "Manual" },
  { value: "skipped", label: "Skipped" },
  { value: "scheduled", label: "Scheduled" },
  { value: "created", label: "Created" },
];
