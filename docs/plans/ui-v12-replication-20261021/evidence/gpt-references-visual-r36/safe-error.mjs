// Playwright transport errors may include request headers, including HttpOnly cookies.
export function safeErrorMessage(error, secrets = []) {
  let message = error instanceof Error ? error.message : String(error);
  for (const secret of secrets) if (secret) message = message.replaceAll(secret, '[REDACTED]');
  return message
    .replace(/(?:cookie|authorization|x-csrf-token):[^\r\n]*/gi, '[REDACTED HEADER]')
    .replace(/__Host-ops-session=[^\s;]+/g, '__Host-ops-session=[REDACTED]');
}
