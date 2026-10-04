import type { CommandExecutionEvent } from './generated/models/commandExecutionEvent';
import type { InvestigationEvent } from './generated/models/investigationEvent';

export type EventDecoder<T> = (value: unknown) => T;

export interface JSONServerSentEvent<T> {
  id?: string;
  event: string;
  data: T;
}

// Use this companion client with the URL builders from generated/platform.ts.
// It yields decoded event envelopes as they arrive instead of buffering the
// open text/event-stream response as the generic generated fetch client does.
export async function* streamJSONEvents<T>(
  url: string,
  decode: EventDecoder<T>,
  options: RequestInit = {},
): AsyncGenerator<JSONServerSentEvent<T>> {
  const headers = new Headers(options.headers);
  headers.set('Accept', 'text/event-stream');
  const response = await fetch(url, { ...options, headers });
  if (!response.ok) {
    const detail = await response.text();
    throw new Error(`SSE request failed with status ${response.status}: ${detail}`);
  }
  const mediaType = (response.headers.get('content-type') ?? '').split(';', 1)[0].trim().toLowerCase();
  if (mediaType !== 'text/event-stream') {
    throw new Error(`SSE response has content type ${mediaType || '(missing)'}`);
  }
  if (!response.body) {
    throw new Error('SSE response has no readable body');
  }
  yield* readJSONEventStream(response.body, decode);
}

export async function* readJSONEventStream<T>(
  body: ReadableStream<Uint8Array>,
  decode: EventDecoder<T>,
): AsyncGenerator<JSONServerSentEvent<T>> {
  let eventName = '';
  let lastEventID: string | undefined;
  let dataLines: string[] = [];

  for await (const line of readSSELines(body)) {
    if (line === '') {
      if (dataLines.length > 0) {
        let value: unknown;
        try {
          value = JSON.parse(dataLines.join('\n'));
        } catch (error) {
          throw new Error('SSE data is not valid JSON', { cause: error });
        }
        const message: JSONServerSentEvent<T> = {
          event: eventName || 'message',
          data: decode(value),
        };
        if (lastEventID !== undefined) message.id = lastEventID;
        yield message;
      }
      eventName = '';
      dataLines = [];
      continue;
    }
    if (line.startsWith(':')) continue;

    const separator = line.indexOf(':');
    const field = separator < 0 ? line : line.slice(0, separator);
    let value = separator < 0 ? '' : line.slice(separator + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    switch (field) {
      case 'data':
        dataLines.push(value);
        break;
      case 'event':
        eventName = value;
        break;
      case 'id':
        if (!value.includes('\0')) lastEventID = value;
        break;
      default:
        break;
    }
  }
}

export function decodeCommandExecutionEvent(value: unknown): CommandExecutionEvent {
  return decodeEventEnvelope<CommandExecutionEvent>(value, ['eventId', 'eventType', 'executionId', 'occurredAt', 'payload'], [
    'eventId', 'eventType', 'executionId', 'occurredAt',
  ]);
}

export function decodeInvestigationEvent(value: unknown): InvestigationEvent {
  return decodeEventEnvelope<InvestigationEvent>(value, ['eventId', 'eventType', 'jobId', 'occurredAt', 'payload'], [
    'eventId', 'eventType', 'jobId', 'occurredAt',
  ]);
}

function decodeEventEnvelope<T>(value: unknown, required: string[], stringFields: string[]): T {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('SSE data must be an event envelope object');
  }
  const record = value as Record<string, unknown>;
  for (const field of required) {
    if (!(field in record)) throw new Error(`SSE event envelope is missing ${field}`);
  }
  for (const field of stringFields) {
    if (typeof record[field] !== 'string' || record[field] === '') {
      throw new Error(`SSE event envelope field ${field} must be a non-empty string`);
    }
  }
  return record as unknown as T;
}

async function* readSSELines(body: ReadableStream<Uint8Array>): AsyncGenerator<string> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let remainder = '';
  let pendingCarriageReturn = false;
  let streamEnded = false;

  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        streamEnded = true;
        let tail = decoder.decode();
        if (pendingCarriageReturn) tail = `\r${tail}`;
        remainder += tail.replace(/\r\n?/g, '\n');
        if (remainder !== '') yield remainder;
        return;
      }

      let text = decoder.decode(value, { stream: true });
      if (pendingCarriageReturn) {
        text = `\r${text}`;
        pendingCarriageReturn = false;
      }
      if (text.endsWith('\r')) {
        text = text.slice(0, -1);
        pendingCarriageReturn = true;
      }
      remainder += text.replace(/\r\n?/g, '\n');
      const lines = remainder.split('\n');
      remainder = lines.pop() ?? '';
      for (const line of lines) yield line;
    }
  } finally {
    try {
      if (!streamEnded) await reader.cancel();
    } finally {
      reader.releaseLock();
    }
  }
}

// Reconnect with the last verified cursor and yield each persisted business
// event once. The caller supplies Bearer headers using the shared fetch client.
export async function* streamInvestigationEventsWithResume(
  url: string,
  options: RequestInit,
  resume: { cursor?: string; eventSeq: bigint } = { eventSeq: 0n },
): AsyncGenerator<JSONServerSentEvent<InvestigationEvent>> {
  const headers = new Headers(options.headers);
  if (!headers.get('Authorization')?.startsWith('Bearer ')) {
    throw new Error('Investigation SSE requires Bearer authentication');
  }
  if (resume.cursor) headers.set('Last-Event-ID', resume.cursor);
  for await (const message of streamJSONEvents(url, decodeInvestigationEvent, { ...options, headers })) {
    const seq = BigInt(message.data.eventId);
    if (seq <= resume.eventSeq) continue;
    if (seq !== resume.eventSeq + 1n) throw new Error('Investigation SSE event sequence gap');
    if (!message.id) throw new Error('Investigation SSE cursor is missing');
    resume.eventSeq = seq;
    resume.cursor = message.id;
    yield message;
  }
}
