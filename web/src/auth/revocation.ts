// Pending native session revocation.
//
// Logout clears the local identity immediately, but the server revocation can
// still fail (500, timeout, offline browser). In that case the cookie may remain
// valid, so the client must remember exactly which server session is
// unconfirmed and offer a controlled retry instead of claiming success. The
// record is browser-local and contains no cookie, token or credential: only the
// opaque session reference, the expected session version and the failure.
export type PendingRevocation = {
  sessionRef: string;
  expectedSessionVersion: number;
  reason: string;
  failedAt: string;
  attempts: number;
  // The CSRF token of the session that must be revoked. It is bound to that
  // session, it is not a credential on its own and it never leaves the browser.
  csrfToken: string;
};

const storageKey = "v12:pending-revocation";

function storage(): Storage | null {
  try {
    return typeof window === "undefined" ? null : window.sessionStorage;
  } catch {
    return null;
  }
}

export function readPendingRevocation(): PendingRevocation | null {
  const raw = storage()?.getItem(storageKey);
  if (!raw) return null;
  try {
    const value = JSON.parse(raw) as Partial<PendingRevocation>;
    if (
      typeof value.sessionRef !== "string" ||
      value.sessionRef === "" ||
      typeof value.expectedSessionVersion !== "number" ||
      value.expectedSessionVersion < 1 ||
      typeof value.reason !== "string" ||
      value.reason === "" ||
      typeof value.csrfToken !== "string" ||
      value.csrfToken === ""
    ) {
      return null;
    }
    return {
      sessionRef: value.sessionRef,
      expectedSessionVersion: value.expectedSessionVersion,
      reason: value.reason,
      failedAt: typeof value.failedAt === "string" ? value.failedAt : "",
      attempts: typeof value.attempts === "number" ? value.attempts : 0,
      csrfToken: value.csrfToken,
    };
  } catch {
    return null;
  }
}

export function writePendingRevocation(value: PendingRevocation): PendingRevocation {
  const next = {
    ...value,
    attempts: value.attempts + 1,
    failedAt: new Date().toISOString(),
  };
  storage()?.setItem(storageKey, JSON.stringify(next));
  return next;
}

export function clearPendingRevocation(): void {
  storage()?.removeItem(storageKey);
}
