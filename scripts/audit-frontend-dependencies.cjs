#!/usr/bin/env node
'use strict';

const { spawnSync } = require('node:child_process');
const path = require('node:path');

const directory = process.argv[2] || path.join(__dirname, '../dexMonitor/react-frontend');
const reviewCommand = `npm --prefix '${directory.replaceAll("'", "'\\''")}' audit --include=dev`;
const warn = message => console.warn(`dev-server: WARNING: ${message}`);

console.log('dev-server: checking frontend dependencies for known vulnerabilities (including dev dependencies)');
const result = spawnSync('npm', [
  'audit', '--json', '--package-lock-only', '--ignore-scripts',
  '--include=dev', '--include=optional', '--include=peer',
  '--fetch-retries=0', '--fetch-timeout=10000',
], {
  cwd: directory,
  encoding: 'utf8',
  timeout: 20000,
  killSignal: 'SIGKILL',
  maxBuffer: 4 * 1024 * 1024,
});

// npm exits 1 for findings as well as some failures: inspect the report too.
let report;
try {
  report = JSON.parse(result.stdout);
} catch {
  // Missing/malformed output is an unavailable audit, never a clean result.
}
const counts = report?.metadata?.vulnerabilities;
const severities = ['critical', 'high', 'moderate', 'low', 'info'];
const validCounts = counts && ['total', ...severities].every(
  severity => Number.isInteger(counts[severity]) && counts[severity] >= 0,
);

if (result.error || ![0, 1].includes(result.status) || report?.error || !validCounts ||
    (result.status === 1 && counts.total === 0)) {
  const reason = result.error?.code === 'ETIMEDOUT' ? 'timed out after 20 seconds' : 'could not complete';
  warn(`dependency audit ${reason}; vulnerability status is unknown. Services remain running.`);
  console.warn(`dev-server: retry manually: ${reviewCommand}`);
} else if (counts.total > 0) {
  const summary = severities.filter(severity => counts[severity] > 0)
    .map(severity => `${counts[severity]} ${severity}`).join(', ');
  warn(`${counts.total} vulnerable frontend dependency packages (${summary}). Review and update affected dependencies.`);
  console.warn(`dev-server: review details: ${reviewCommand}`);
} else {
  console.log('dev-server: frontend dependency audit found no known vulnerabilities');
}
