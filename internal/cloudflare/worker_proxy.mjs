// Fixed origin, streaming bodies, manual redirects and no shared cache.
// The Go publisher appends the default export with validated JSON literals.
/** @param {string} originURL @param {string} entryHostname @returns {ExportedHandler} */
export function createProxy(originURL, entryHostname) {
  const origin = new URL(originURL);
  return {
    async fetch(request) {
      const incoming = new URL(request.url);
      if (incoming.hostname !== entryHostname) return new Response('Not found', { status: 404 });
      if (incoming.protocol !== 'https:') {
        incoming.protocol = 'https:';
        incoming.port = '';
        return Response.redirect(incoming.href, 308);
      }
      if (request.method === 'CONNECT' || request.method === 'TRACE') {
        return new Response('Method not allowed', { status: 405 });
      }
      const target = new URL(origin.href);
      target.pathname = incoming.pathname;
      target.search = incoming.search;
      const headers = new Headers(request.headers);
      headers.delete('host');
      headers.delete('cf-access-client-id');
      headers.delete('cf-access-client-secret');
      headers.set('x-forwarded-host', entryHostname);
      headers.set('x-forwarded-proto', 'https');
      try {
        const upstream = await fetch(target.href, {
          method: request.method, headers,
          body: request.method === 'GET' || request.method === 'HEAD' ? null : request.body,
          redirect: 'manual', cache: 'no-store',
        });
        if (upstream.status === 101) return upstream;
        const response = new Response(upstream.body, upstream);
        response.headers.set('cache-control', 'private, no-store');
        const location = response.headers.get('location');
        if (location) {
          const redirect = new URL(location, target);
          if (redirect.origin === origin.origin) {
            redirect.protocol = 'https:';
            redirect.hostname = entryHostname;
            redirect.port = '';
            response.headers.set('location', redirect.href);
          }
        }
        return response;
      } catch {
        return new Response('Origin unavailable', { status: 502, headers: { 'cache-control': 'no-store' } });
      }
    },
  };
}
