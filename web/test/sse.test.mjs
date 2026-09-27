import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const readerURL = new URL('../src/api/sse-reader.ts', import.meta.url);

const validExecutionEvent = {
  eventId: 'evt-1',
  eventType: 'output',
  executionId: 'exec-1',
  occurredAt: '2026-09-27T00:00:00Z',
  payload: { text: '节点故障' },
};

test('typed SSE reader yields a decoded envelope before the stream closes', async () => {
  assert.ok(existsSync(fileURLToPath(readerURL)), 'typed SSE reader is not implemented');
  const { readJSONEventStream, decodeCommandExecutionEvent } = await import(readerURL.href);
  const encoder = new TextEncoder();
  const body = new ReadableStream({
    start(streamController) {
      const first = 'id: 17\r\nevent: execution\r\ndata: {"eventId":"evt-1","eventType":"output",\r\ndata: "executionId":"exec-1","occurredAt":"2026-09-27T00:00:00Z",\r\ndata: "payload":{"text":"节点故障"}}\r\n\r\n';
      const bytes = encoder.encode(first);
      streamController.enqueue(bytes.slice(0, 29));
      streamController.enqueue(bytes.slice(29));
    },
  });
  const iterator = readJSONEventStream(body, decodeCommandExecutionEvent);
  let timeout;
  const result = await Promise.race([
    iterator.next(),
    new Promise((_, reject) => {
      timeout = setTimeout(() => reject(new Error('SSE reader waited for stream completion')), 1000);
    }),
  ]);
  clearTimeout(timeout);

  assert.deepEqual(result, {
    done: false,
    value: { id: '17', event: 'execution', data: validExecutionEvent },
  });
  await iterator.return();
});

test('typed SSE fetch client requests and parses text/event-stream', async () => {
  assert.ok(existsSync(fileURLToPath(readerURL)), 'typed SSE reader is not implemented');
  const { streamJSONEvents, decodeCommandExecutionEvent } = await import(readerURL.href);
  const originalFetch = globalThis.fetch;
  let requestHeaders;
  globalThis.fetch = async (_url, options) => {
    requestHeaders = new Headers(options.headers);
    return new Response(`data: ${JSON.stringify(validExecutionEvent)}\n\n`, {
      headers: { 'content-type': 'text/event-stream; charset=utf-8' },
    });
  };
  try {
    const events = [];
    for await (const event of streamJSONEvents('/events', decodeCommandExecutionEvent)) {
      events.push(event);
    }
    assert.equal(requestHeaders.get('accept'), 'text/event-stream');
    assert.deepEqual(events, [{ event: 'message', data: validExecutionEvent }]);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('typed SSE envelope decoder rejects incomplete event data', async () => {
  assert.ok(existsSync(fileURLToPath(readerURL)), 'typed SSE reader is not implemented');
  const { decodeCommandExecutionEvent } = await import(readerURL.href);
  const incomplete = { ...validExecutionEvent };
  delete incomplete.executionId;
  assert.throws(() => decodeCommandExecutionEvent(incomplete), /executionId/);
});
