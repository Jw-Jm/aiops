import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { requestOptions } from "../../api/client";
import { getGetAssistantConversationEventsUrl } from "../../api/generated/platform";
import type { AssistantEventV1 } from "../../api/generated/models";
import { SSEHttpError, streamJSONEvents } from "../../api/sse-reader";
function decode(value: unknown): AssistantEventV1 {
  if (!value || typeof value !== "object") throw new Error("无效会话事件");
  const event = value as AssistantEventV1;
  if (
    !Number.isSafeInteger(event.eventId) ||
    typeof event.turnId !== "string" ||
    typeof event.type !== "string" ||
    typeof event.occurredAt !== "string" ||
    !event.payload ||
    typeof event.payload !== "object"
  )
    throw new Error("无效会话事件");
  return event;
}
export function useConversationStream(
  id: string | undefined,
  sessionKey: string,
) {
  const client = useQueryClient();
  const cursor = useRef<string | undefined>(undefined);
  const [error, setError] = useState<Error>();
  useEffect(() => {
    cursor.current = undefined;
    setError(undefined);
    if (!id) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const refresh = (all = false) => {
      void client.invalidateQueries({
        queryKey: all
          ? ["assistant", sessionKey]
          : ["assistant", sessionKey, id],
      });
    };
    const connect = async () => {
      try {
        const options = requestOptions(controller.signal);
        const headers = new Headers(options.headers);
        if (cursor.current) headers.set("Last-Event-ID", cursor.current);
        for await (const event of streamJSONEvents(
          getGetAssistantConversationEventsUrl(id),
          decode,
          { ...options, headers },
        )) {
          if (controller.signal.aborted) return;
          cursor.current = event.id;
          // Reading retained evidence is audited, but does not change it.
          if (event.data.type !== "turn.result_read")
            refresh(!event.data.type.startsWith("turn.step_"));
          setError(undefined);
        }
      } catch (cause) {
        if (controller.signal.aborted) return;
        const err = cause instanceof Error ? cause : new Error("事件连接中断");
        setError(err);
        if (err instanceof SSEHttpError && [401, 403].includes(err.status)) {
          client.removeQueries({ queryKey: ["assistant", sessionKey] });
          return;
        }
        if (err instanceof SSEHttpError && err.status === 410) {
          cursor.current = undefined;
          refresh(true);
        }
      }
      if (!controller.signal.aborted)
        timer = setTimeout(() => void connect(), 2000);
    };
    void connect();
    return () => {
      controller.abort();
      if (timer) clearTimeout(timer);
    };
  }, [id, sessionKey, client]);
  return error;
}
