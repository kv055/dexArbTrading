import assert from 'node:assert/strict';

// These namespaces are forwarded without response transformation by dexMonitor.
export function upstreamPath(path) {
  if (path.startsWith('/api/mm-controller/')) return path.replace('/api/mm-controller/', '/api/');
  return path.match(/^\/api\/trading\/accounts\/\{id\}\/cex\//)
    ? path.replace('/api/trading/accounts/{id}/cex/', '/api/cex/')
    : null;
}

function expand(value, document, seen = new Set()) {
  if (!value || typeof value !== 'object') return value;
  if (value.$ref) {
    const ref = value.$ref;
    if (!ref.startsWith('#/')) throw new Error('Proxy check requires bundled contracts: ' + ref);
    if (seen.has(ref)) throw new Error('Recursive proxy schema requires explicit comparison: ' + ref);
    const target = ref.slice(2).split('/').reduce((node, key) => node?.[key.replaceAll('~1', '/').replaceAll('~0', '~')], document);
    if (!target) throw new Error('Unresolved reference: ' + ref);
    return expand({ ...target, ...Object.fromEntries(Object.entries(value).filter(([key]) => key !== '$ref')) }, document, new Set([...seen, ref]));
  }
  if (Array.isArray(value)) return value.map(item => expand(item, document, seen));
  return Object.fromEntries(Object.entries(value)
    .map(([key, item]) => [key, expand(item, document, seen)]));
}

export function checkProxyContracts(publicSpec, upstreamSpec) {
  let checked = 0;
  for (const [path, item] of Object.entries(publicSpec.paths)) {
    const target = upstreamPath(path);
    if (!target) continue;
    for (const method of ['get', 'post', 'put', 'patch', 'delete']) {
      if (!item[method]) continue;
      const upstream = upstreamSpec.paths[target]?.[method];
      assert.ok(upstream, method.toUpperCase() + ' ' + path + ': missing upstream operation ' + target);
      // Compare each explicitly documented successful response. Raw transports
      // still need accurate consumer types even though runtime validation is bypassed.
      for (const [status, response] of Object.entries(item[method].responses)) {
        if (!/^2\d\d$/.test(status)) continue;
        const expected = upstream.responses[status];
        assert.ok(expected, method.toUpperCase() + ' ' + path + ': upstream does not declare ' + status);
        assert.deepStrictEqual(expand(response.content, publicSpec), expand(expected.content, upstreamSpec),
          method.toUpperCase() + ' ' + path + ' response ' + status + ' differs from ' + target);
        checked++;
      }
    }
  }
  return checked;
}
