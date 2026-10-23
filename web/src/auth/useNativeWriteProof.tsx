import { useEffect, useState } from "react";
import { requestOptions } from "../api/client";
import { useAuth } from "./AuthProvider";
import {
  NativeStepUpControl,
  type NativeWriteIntent,
} from "./NativeStepUpControl";

export function administrativeWrite(
  operation: NativeWriteIntent["operation"],
  method: string,
  path: string,
  body: unknown,
): NativeWriteIntent {
  return {
    operation,
    target: { kind: "administrative_write" },
    write: { method, path, body },
  };
}

export function useNativeWriteProof(
  intent: NativeWriteIntent | undefined,
  oidcReady: boolean,
) {
  const auth = useAuth();
  const native = auth.authMode === "native";
  const stamp = JSON.stringify([
    intent,
    auth.nativeSession?.sessionRef,
    auth.nativeSession?.revocation,
  ]);
  const [proof, setProof] = useState<{
    stamp: string;
    id: string;
    expires: string;
  }>();
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!proof) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [proof]);
  const ready = native
    ? !!intent && proof?.stamp === stamp && Date.parse(proof.expires) > now
    : oidcReady;
  return {
    ready,
    options: () => {
      const options = requestOptions(undefined, true);
      if (!native) return options;
      if (
        !intent ||
        proof?.stamp !== stamp ||
        Date.parse(proof.expires) <= Date.now()
      )
        throw new Error("当前操作的验证已失效，请重新验证。");
      return {
        ...options,
        headers: { ...options.headers, "X-Ops-Step-Up-Challenge": proof.id },
      };
    },
    verification:
      native && intent ? (
        <NativeStepUpControl
          key={stamp}
          intent={intent}
          onReady={(expires, id) => {
            setNow(Date.now());
            setProof({ stamp, id, expires });
          }}
        />
      ) : null,
  };
}
