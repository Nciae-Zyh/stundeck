import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createProxy } from './worker_proxy.mjs';

const proxy = createProxy('http://origin.example.com:54321', 'nas.example.com');

test('fixed origin retains encoded paths and never follows redirects with credentials', async (t) => {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    calls.push({ url, options });
    assert.equal(await new Response(options.body).text(), 'streamed body');
    return new Response('redirect', { status: 302, headers: { location: 'https://unrelated.example/path' } });
  });
  const response = await proxy.fetch(new Request('https://nas.example.com//evil.test/a%2Fb?url=https://evil.test', {
    method: 'POST', body: 'streamed body', headers: { authorization: 'app-auth', 'cf-access-client-secret': 'test-secret', 'x-forwarded-host': 'spoofed' },
  }));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, 'http://origin.example.com:54321//evil.test/a%2Fb?url=https://evil.test');
  assert.equal(calls[0].options.redirect, 'manual');
  assert.equal(calls[0].options.cache, 'no-store');
  assert.equal(calls[0].options.headers.get('authorization'), 'app-auth');
  assert.equal(calls[0].options.headers.get('cf-access-client-secret'), null);
  assert.equal(calls[0].options.headers.get('x-forwarded-host'), 'nas.example.com');
  assert.equal(response.headers.get('location'), 'https://unrelated.example/path');
  assert.equal(response.headers.get('cache-control'), 'private, no-store');
});

test('same-origin redirects remain behind the public Access hostname', async (t) => {
  t.mock.method(globalThis, 'fetch', async () => new Response(null, { status: 302, headers: { location: '/login?next=%2F' } }));
  const response = await proxy.fetch(new Request('https://nas.example.com/private'));
  assert.equal(response.headers.get('location'), 'https://nas.example.com/login?next=%2F');
});

test('alternate worker hostname cannot bypass Access and HTTP redirects to HTTPS', async (t) => {
  t.mock.method(globalThis, 'fetch', () => { assert.fail('must not fetch origin'); });
  const denied = await proxy.fetch(new Request('https://worker.workers.dev/private', { headers: { host: 'nas.example.com' } }));
  assert.equal(denied.status, 404);
  const redirect = await proxy.fetch(new Request('http://nas.example.com/path?q=1'));
  assert.equal(redirect.status, 308);
  assert.equal(redirect.headers.get('location'), 'https://nas.example.com/path?q=1');
});

test('streaming and WebSocket responses are not buffered', async (t) => {
  const body = new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode('event: update\ndata: one\n\n')); controller.close(); } });
  t.mock.method(globalThis, 'fetch', async () => new Response(body, { headers: { 'content-type': 'text/event-stream' } }));
  const response = await proxy.fetch(new Request('https://nas.example.com/events'));
  assert.equal(response.headers.get('content-type'), 'text/event-stream');
  assert.equal(await response.text(), 'event: update\ndata: one\n\n');
  const websocketResponse = { status: 101, webSocket: {} };
  globalThis.fetch.mock.mockImplementation(async () => websocketResponse);
  assert.equal(await proxy.fetch(new Request('https://nas.example.com/socket', { headers: { upgrade: 'websocket' } })), websocketResponse);
});

test('origin failures return a generic closed error', async (t) => {
  t.mock.method(globalThis, 'fetch', async () => { throw new Error('sensitive internal details'); });
  const response = await proxy.fetch(new Request('https://nas.example.com/'));
  assert.equal(response.status, 502);
  assert.equal(await response.text(), 'Origin unavailable');
});
