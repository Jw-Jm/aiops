import { Button, Tooltip } from "antd";
import { Link } from "react-router-dom";
import { assistantEntryHref, type AssistantEntry } from "./scope";

// The entry that asks the assistant about the scope the operator is looking
// at. The link carries the cluster, the real resource identity and the
// observed time window, so the assistant starts in the scope of the page and
// not in a default scope.
//
// The entry never fills a question and never starts a session by itself: the
// assistant opens its own new-session form, where the mode, the scope, the
// window and the model are confirmed by the operator.
export function AskAssistantEntry({
  entry,
  label = "按此范围询问 AI",
  disabled,
  reason,
  primary,
  type = "default",
}: {
  entry: AssistantEntry;
  label?: string;
  disabled?: boolean;
  reason?: string;
  primary?: boolean;
  type?: "default" | "primary" | "text" | "link";
}) {
  const button = (
    <Button
      type={primary ? "primary" : type}
      disabled={disabled}
      aria-label={label}
    >
      {label}
    </Button>
  );
  if (disabled)
    return reason ? (
      <Tooltip title={reason}>{button}</Tooltip>
    ) : (
      button
    );
  return <Link to={assistantEntryHref(entry)}>{button}</Link>;
}
