import assert from 'node:assert/strict';
import fs from 'node:fs';
import { test } from 'node:test';
import { parse } from 'yaml';
import { checkProxyContracts } from './proxy-contracts.mjs';

const read = path => parse(fs.readFileSync(new URL(path, import.meta.url), 'utf8'));
const monitor = read('../../api-docs/specs/dex-monitor/public.openapi.yaml');
const mm = read('../../api-docs/specs/dex-mm-controller/internal.openapi.yaml');

test('bundled proxy contracts agree, including raw routes', () => {
  assert.ok(checkProxyContracts(monitor, mm) > 40);
});

test('original deposit-address object mistake fails the CI guard', () => {
  const broken = structuredClone(monitor);
  broken.paths['/api/trading/accounts/{id}/cex/deposit-addresses'].get.responses['200'].content['application/json'].schema = { type: 'object', additionalProperties: true };
  assert.throws(() => checkProxyContracts(broken, mm), /deposit-addresses.*differs/);
});

test('raw proxy response drift also fails', () => {
  const broken = structuredClone(monitor);
  broken.paths['/api/mm-controller/lnd/submarine-swaps'].get.responses['200'].content['application/json'].schema = { type: 'object' };
  assert.throws(() => checkProxyContracts(broken, mm), /submarine-swaps.*differs/);
});

test('missing upstream route fails rather than silently skipping comparison', () => {
  const broken = structuredClone(mm);
  delete broken.paths['/api/cex/deposit-addresses'];
  assert.throws(() => checkProxyContracts(monitor, broken), /missing upstream/);
});
