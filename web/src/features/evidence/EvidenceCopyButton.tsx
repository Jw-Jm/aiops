import { useEffect, useRef, useState } from "react";
import { Button } from "antd";
import { useAuth } from "../../auth/AuthProvider";
import { useOperationalCluster } from "../../app/ClusterContext";
import { checked, requestOptions } from "../../api/client";
import { getEvidence } from "../../api/generated/platform";
import { QueryState } from "../../components/QueryState";

export function EvidenceCopyButton({ evidenceId }: { evidenceId: string }) {
  const auth = useAuth();
  const { scopeKey } = useOperationalCluster();
  const identity = JSON.stringify([
    scopeKey,
    auth.session?.subject,
    auth.nativeSession?.sessionRef,
    evidenceId,
  ]);
  const current = useRef(identity);
  current.current = identity;
  const active = useRef<AbortController | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<Error>();
  useEffect(() => {
    setBusy(false);
    setCopied(false);
    setError(undefined);
    return () => {
      active.current?.abort();
      active.current = undefined;
    };
  }, [identity]);
  async function copy() {
    if (active.current) return;
    const controller = new AbortController();
    const frozen = current.current;
    active.current = controller;
    setBusy(true);
    setError(undefined);
    try {
      const response = await checked(
        getEvidence(evidenceId, requestOptions(controller.signal)),
      );
      if (controller.signal.aborted || frozen !== current.current) return;
      if (
        response.data.evidenceId !== evidenceId ||
        response.data.factSlice === undefined
      )
        throw new Error("证据身份或观测内容不完整，请重新读取");
      const content = response.data.factSlice;
      await navigator.clipboard.writeText(
        typeof content === "string"
          ? content
          : JSON.stringify(content, null, 2),
      );
      if (!controller.signal.aborted && frozen === current.current)
        setCopied(true);
    } catch (failure) {
      if (!controller.signal.aborted && frozen === current.current)
        setError(failure as Error);
    } finally {
      if (!controller.signal.aborted && frozen === current.current)
        setBusy(false);
      if (active.current === controller) active.current = undefined;
    }
  }
  return (
    <>
      <Button loading={busy} onClick={() => void copy()}>
        {copied ? "已复制证据内容" : "复制证据内容"}
      </Button>
      {error && <QueryState loading={false} error={error} />}
    </>
  );
}
