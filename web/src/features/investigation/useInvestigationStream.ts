import { abortDelay } from "./delay";
import { useEffect, useRef, useState } from "react";
import {
  streamInvestigationEventsWithResume,
  SSEHttpError,
} from "../../api/sse-reader";
import { getStreamInvestigationEventsUrl } from "../../api/generated/platform";
import type { InvestigationEvent } from "../../api/generated/models";
import { requestOptions, clearCredentials, authEvents } from "../../api/client";
export function useInvestigationStream(
  jobId: string | undefined,
  active: boolean,
  onEvent: () => void,
) {
  const [events, setEvents] = useState<InvestigationEvent[]>([]);
  const [status, setStatus] = useState("idle");
  const [error, setError] = useState<Error>();
  const [restart, setRestart] = useState(0);
  const streamState = useRef({
    jobId,
    resume: { eventSeq: 0n, cursor: undefined as string | undefined },
  });
  const notify = useRef(onEvent);
  notify.current = onEvent;
  useEffect(() => {
    if (streamState.current.jobId !== jobId) {
      streamState.current = {
        jobId,
        resume: { eventSeq: 0n, cursor: undefined },
      };
      setEvents([]);
    }
    setError(undefined);
    const controller = new AbortController();
    const resume = streamState.current.resume;
    let stopped = false;
    let attempts = 0;
    const abort = () => {
      stopped = true;
      controller.abort();
      resume.eventSeq = 0n;
      resume.cursor = undefined;
      setEvents([]);
      setStatus("permission_changed");
    };
    const wait = (ms: number) => abortDelay(controller.signal, ms);
    authEvents.addEventListener("expired", abort);
    authEvents.addEventListener("forbidden", abort);
    authEvents.addEventListener("authorizationChanged", abort);
    const cleanup = () => {
      stopped = true;
      controller.abort();
      authEvents.removeEventListener("expired", abort);
      authEvents.removeEventListener("forbidden", abort);
      authEvents.removeEventListener("authorizationChanged", abort);
    };
    if (!jobId || !active) {
      setStatus("idle");
      return cleanup;
    }
    async function run() {
      while (!stopped && attempts < 10) {
        try {
          setStatus(resume.cursor ? "reconnecting" : "connecting");
          for await (const message of streamInvestigationEventsWithResume(
            getStreamInvestigationEventsUrl(jobId!),
            requestOptions(controller.signal),
            resume,
          )) {
            if (stopped) return;
            setStatus("connected");
            setError(undefined);
            attempts = 0;
            setEvents((previous) => [...previous, message.data].slice(-100));
            notify.current();
          }
          if (stopped) return;
          setStatus("reconnecting");
        } catch (e) {
          if (stopped) return;
          setError(e as Error);
          if (e instanceof SSEHttpError && [401, 403, 410].includes(e.status)) {
            resume.eventSeq = 0n;
            resume.cursor = undefined;
            setEvents([]);
            if (e.status === 401) clearCredentials();
            if (e.status === 403)
              authEvents.dispatchEvent(new Event("forbidden"));
            if (e.status === 410) notify.current();
            setStatus(
              e.status === 410 ? "cursor_expired" : "permission_revoked",
            );
            return;
          }
        }
        attempts++;
        await wait(Math.min(10000, 1000 * 2 ** Math.min(attempts, 4)));
      }
      if (!stopped) setStatus("disconnected");
    }
    void run();
    return cleanup;
  }, [jobId, active, restart]);
  return { events, status, error, reconnect: () => setRestart((n) => n + 1) };
}
