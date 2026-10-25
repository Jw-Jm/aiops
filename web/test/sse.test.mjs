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

test('investigation fetch SSE resumes with Bearer and durable cursor, suppresses duplicate event sequences', async () => {
  const { streamInvestigationEventsWithResume } = await import(readerURL.href);
  const originalFetch = globalThis.fetch;
  const resume = { cursor: 'signed-first-cursor', eventSeq: 1n };
  let headers;
  globalThis.fetch = async (url, options) => {
    assert.equal(url, '/api/v1/investigations/owned-job/events');
    assert.ok(!url.includes('token='));
    headers = new Headers(options.headers);
    return new Response([1,2,2,3].map(seq => `id: signed-${seq}\ndata: ${JSON.stringify({ eventId: String(seq), eventType: 'step.completed', jobId: 'owned-job', occurredAt: '2026-10-03T00:00:00Z', payload: {} })}\n\n`).join(''), { headers: { 'content-type': 'text/event-stream' } });
  };
  try {
    const events = [];
    for await (const message of streamInvestigationEventsWithResume('/api/v1/investigations/owned-job/events', { headers: { Authorization: 'Bearer owned-token' } }, resume)) events.push(message.data.eventId);
    assert.deepEqual(events, ['2','3']);
    assert.equal(headers.get('Authorization'),'Bearer owned-token');
    assert.equal(headers.get('Last-Event-ID'),'signed-first-cursor');
    assert.deepEqual(resume,{cursor:'signed-3',eventSeq:3n});
  } finally { globalThis.fetch=originalFetch; }
});

test('investigation SSE streams a native cookie session and fails closed on sequence gaps and 410', async () => {
 const { streamInvestigationEventsWithResume } = await import(readerURL.href);
 const originalFetch=globalThis.fetch;
 try {
  let seenOptions=null;
  globalThis.fetch=async(_url,options)=>{seenOptions=options;return new Response(`id: signed-1\ndata: ${JSON.stringify({eventId:'1',eventType:'job.state',jobId:'j',occurredAt:'now',payload:{}})}\n\n`,{headers:{'content-type':'text/event-stream'}});};
  const events=[];
  for await(const event of streamInvestigationEventsWithResume('/events',{credentials:'same-origin',headers:{}})){events.push(event.data.eventId);}
  assert.deepEqual(events,['1']);
  assert.equal(seenOptions.credentials,'same-origin');
  assert.equal(new Headers(seenOptions.headers).get('Authorization'),null);
  globalThis.fetch=async()=>new Response(`id: signed-2\ndata: ${JSON.stringify({eventId:'2',eventType:'job.state',jobId:'j',occurredAt:'now',payload:{}})}\n\n`,{headers:{'content-type':'text/event-stream'}});
  await assert.rejects(async()=>{for await(const event of streamInvestigationEventsWithResume('/events',{headers:{Authorization:'Bearer token'}})){}},/sequence gap/);
  globalThis.fetch=async()=>new Response('{}',{status:410});
  await assert.rejects(async()=>{for await(const event of streamInvestigationEventsWithResume('/events',{headers:{Authorization:'Bearer token'}})){}},/410/);
  globalThis.fetch=async()=>new Response('{}',{status:401});
  await assert.rejects(async()=>{for await(const event of streamInvestigationEventsWithResume('/events',{credentials:'same-origin',headers:{}})){}},/401/);
 }finally{globalThis.fetch=originalFetch;}
});

test('SSE discards already-buffered events when its request scope is revoked', async () => {
  const { streamJSONEvents, decodeCommandExecutionEvent } = await import(readerURL.href);
  const originalFetch = globalThis.fetch;
  const controller = new AbortController();
  let cancelled = false;
  globalThis.fetch = async () => new Response(new ReadableStream({
    start(c) { c.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(validExecutionEvent)}\n\ndata: ${JSON.stringify(validExecutionEvent)}\n\n`)); },
    cancel() { cancelled = true; },
  }), { headers: { 'content-type': 'text/event-stream' } });
  try {
    const iterator = streamJSONEvents('/events', decodeCommandExecutionEvent, { signal: controller.signal });
    assert.equal((await iterator.next()).value.data.eventId, 'evt-1');
    controller.abort();
    await assert.rejects(iterator.next(), { name: 'AbortError' });
    assert.equal(cancelled, true);
  } finally { globalThis.fetch = originalFetch; }
});
