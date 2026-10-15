#!/usr/bin/env node
'use strict';

const { spawnSync } = require('node:child_process');
const path = require('node:path');

const directory = process.argv[2] || path.join(__dirname, '../tools/openapi');

// Advisories with no patched release upstream. Each entry must carry a review
// date or removal condition; drop entries as upstream ships fixes.
const ALLOWED_ADVISORIES = new Set([
  // GHSA-vfj7-8cjw-p6xm: braces stack-exhaustion DoS, vulnerable range
  // "<=3.0.3" covers every released version (no fix available). Not exploitable
  // here: braces only processes this repo's own committed OpenAPI patterns via
  // spectral-cli. Remove once braces publishes a patched release.
  'GHSA-vfj7-8cjw-p6xm',
]);

const SEVERITY_ORDER = new Map([
  ['info', 0],
  ['low', 1],
  ['moderate', 2],
  ['high', 3],
  ['critical', 4],
]);
const GATE_SEVERITY = 'moderate';

console.log(`audit-openapi: auditing npm dependencies in ${directory}`);
const result = spawnSync('npm', [
  'audit', '--json', '--ignore-scripts',
], {
  cwd: directory,
  encoding: 'utf8',
  timeout: 60000,
  killSignal: 'SIGKILL',
  maxBuffer: 8 * 1024 * 1024,
});

// npm exits 1 for findings as well as some failures: inspect the report too.
let report;
try {
  report = JSON.parse(result.stdout);
} catch {
  // Missing/malformed output is an unavailable audit, never a clean result.
  console.error('audit-openapi: ERROR: npm audit produced no readable report');
  if (result.stderr) console.error(result.stderr);
  process.exit(1);
}

const gateLevel = SEVERITY_ORDER.get(GATE_SEVERITY);

// An advisory's GHSA id sits in `url`; older audit payloads used `id`.
const advisoryIdOf = entry => entry.id ?? (entry.url ? entry.url.split('/').pop() : undefined);

// A finding is allowed when every `via` entry is allowed: an allowlisted
// advisory directly, or a string reference to a dependency whose own finding
// is allowed (resolves the transitive chain rooted at the advisory).
const vulns = report.vulnerabilities ?? {};
const allowedMemo = new Map();
const allowedStack = new Set();
function isAllowed(name) {
  if (allowedMemo.has(name)) return allowedMemo.get(name);
  if (allowedStack.has(name)) return false; // cycle guard
  allowedStack.add(name);
  const vuln = vulns[name];
  if (vuln == null) {
    allowedMemo.set(name, false);
    return false;
  }
  const viaEntries = vuln.via ?? [];
  const allowed = viaEntries.length > 0 && viaEntries.every(entry => {
    if (typeof entry === 'string') return isAllowed(entry);
    const id = advisoryIdOf(entry);
    return id != null && ALLOWED_ADVISORIES.has(id);
  });
  allowedStack.delete(name);
  allowedMemo.set(name, allowed);
  return allowed;
}

const violations = [];
for (const [name, vuln] of Object.entries(vulns)) {
  const level = SEVERITY_ORDER.get(vuln.severity) ?? 0;
  if (level < gateLevel) continue;
  if (isAllowed(name)) {
    console.log(`audit-openapi: allowed ${name} (${vuln.severity})`);
    continue;
  }
  const via = (vuln.via ?? [])
    .map(entry => (typeof entry === 'string' ? entry : advisoryIdOf(entry) ?? 'unknown'))
    .join(', ');
  violations.push(`  ${name} (${vuln.severity}, via ${via})`);
}

if (violations.length > 0) {
  console.error(`audit-openapi: FAIL: ${violations.length} dependency finding(s) at or above ${GATE_SEVERITY} severity:`);
  for (const line of violations) console.error(line);
  console.error('audit-openapi: fix or add an explicit, justified allowlist entry in scripts/audit-openapi-dependencies.cjs');
  process.exit(1);
}

console.log('audit-openapi: OK: no unexpected findings at or above moderate severity');
