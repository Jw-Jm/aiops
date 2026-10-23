import type { ReactNode } from "react";
import { PageHeader } from "./v12/primitives";

export function PageHeading({
  title,
  description,
  extra,
  kicker,
}: {
  title: string;
  description: string;
  extra?: ReactNode;
  kicker?: string;
}) {
  return (
    <PageHeader
      kicker={kicker}
      title={title}
      description={description}
      actions={extra}
    />
  );
}
