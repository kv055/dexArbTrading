import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { parse, stringify } from 'yaml';

const root = path.resolve(import.meta.dirname, '../..');
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'api-contracts.json'), 'utf8'));
const npmBin = path.join(import.meta.dirname, 'node_modules/.bin');
const redocly = path.join(npmBin, 'redocly');
const spectral = path.join(npmBin, 'spectral');

function validateManifest() {
  if (manifest.version !== 1 || !Array.isArray(manifest.contracts) || !manifest.contracts.length) {
    throw new Error('api-contracts.json must contain a non-empty version 1 contract list');
  }
  const ids = new Set();
  const sources = new Set();
  const bundles = new Set();
  for (const contract of manifest.contracts) {
    if (!contract.id || ids.has(contract.id)) throw new Error(`duplicate or missing contract id: ${contract.id}`);
    if (!['public', 'internal'].includes(contract.audience)) throw new Error(`${contract.id}: invalid audience`);
    if (!['validated-legacy', 'native-strict'].includes(contract.migrationState)) {
      throw new Error(`${contract.id}: invalid migrationState ${contract.migrationState}`);
    }
    if (sources.has(contract.source) || bundles.has(contract.bundle)) {
      throw new Error(`${contract.id}: source and bundle paths must be unique`);
    }
    if (!fs.existsSync(path.join(root, contract.source))) throw new Error(`${contract.id}: source is missing`);
    ids.add(contract.id);
    sources.add(contract.source);
    bundles.add(contract.bundle);
  }
}

validateManifest();

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, stdio: 'inherit', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}

function bundle() {
  for (const contract of manifest.contracts) {
    fs.mkdirSync(path.dirname(path.join(root, contract.bundle)), { recursive: true });
    run(redocly, [
      'bundle', contract.source,
      '--output', contract.bundle,
      '--component-renaming-conflicts-severity', 'error',
      '--remove-unused-components',
    ]);
  }
}

function lint() {
  bundle();
  run(spectral, ['lint', '--ruleset', '.spectral.yaml', ...manifest.contracts.map((contract) => contract.bundle)]);
}

const generatedPackage = {
  public: 'publicapi',
  internal: 'internalapi',
};

function generatedLocations(contract) {
  const leaf = contract.id === 'bisqstats-public'
    ? 'bisqstats'
    : contract.audience === 'internal' ? 'internalapi' : contract.audience;
  return {
    server: `${contract.service}/internal/api/gen/${leaf}/api.gen.go`,
    bridge: `${contract.service}/internal/api/gen/${leaf}/legacy_bridge.gen.go`,
    bridgeTest: `${contract.service}/internal/api/gen/${leaf}/legacy_bridge.gen_test.go`,
    client: `commons/api/client/${contract.id.replaceAll('-', '')}/client.gen.go`,
  };
}

function generateLegacyBridge(contract, locations, packageName) {
  const source = fs.readFileSync(path.join(root, locations.server), 'utf8');
  const interfaceBody = source.match(/type StrictServerInterface interface \{([\s\S]*?)\n\}/)?.[1];
  if (!interfaceBody) throw new Error(`${contract.id}: generated StrictServerInterface not found`);

  const methodPattern = /^\t(\w+)\(ctx context\.Context, request (\w+)\) \((\w+), error\)$/gm;
  const methods = [...interfaceBody.matchAll(methodPattern)].map((match) => ({
    name: match[1],
    request: match[2],
    response: match[3],
  }));
  if (!methods.length) throw new Error(`${contract.id}: generated StrictServerInterface has no operations`);

  const spec = parse(fs.readFileSync(path.join(root, contract.source), 'utf8'));
  const responseProbes = [];
  let defaultResponseProbe;
  for (const [routePath, pathItem] of Object.entries(spec.paths ?? {})) {
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!['get', 'put', 'post', 'delete', 'patch'].includes(method)) continue;
      if (operation.tags?.includes('raw')) continue;
      const success = Object.entries(operation.responses ?? {}).find(([status, response]) =>
        /^2\d\d$/.test(status) && response?.content?.['application/json']?.schema
      );
      if (success) {
        responseProbes.push({
          method: method.toUpperCase(),
          path: routePath.replaceAll(/\{[^}]+\}/g, '1'),
          status: Number(success[0]),
          operationId: operation.operationId,
        });
      }
      if (!defaultResponseProbe && operation.responses?.default?.content?.['text/plain']) {
        defaultResponseProbe = {
          method: method.toUpperCase(),
          path: routePath.replaceAll(/\{[^}]+\}/g, '1'),
        };
      }
    }
  }
  if (!responseProbes.length || !defaultResponseProbe) {
    throw new Error(`${contract.id}: no JSON success/text default operation available for bridge tests`);
  }
  const responseProbeCases = responseProbes.map((probe) =>
    `\t\t{name: ${JSON.stringify(probe.operationId)}, method: ${JSON.stringify(probe.method)}, path: ${JSON.stringify(probe.path)}, status: ${probe.status}},`
  ).join('\n');

  const responseTypes = methods.map((method) => `
type legacy${method.name}Response struct {
\tserver  *LegacyStrictServer
\trequest *legacyRequest
}

func (response legacy${method.name}Response) Visit${method.name}Response(w http.ResponseWriter) error {
\treturn response.server.serveLegacy(w, response.request)
}`).join('');
  const implementations = methods.map((method) => `
func (server *LegacyStrictServer) ${method.name}(ctx context.Context, _ ${method.request}) (${method.response}, error) {
\trequest, err := legacyRequestFromContext(ctx)
\tif err != nil {
\t\treturn nil, err
\t}
\treturn legacy${method.name}Response{server: server, request: request}, nil
}`).join('');

  const generated = `// Code generated by tools/openapi/contracts.mjs; DO NOT EDIT.

package ${packageName}

import (
\t"bytes"
\t"context"
\t"fmt"
\t"io"
\t"net/http"
\t"net/http/httptest"

\t"github.com/getkin/kin-openapi/openapi3filter"
\t"github.com/getkin/kin-openapi/routers"
\tlegacyrouter "github.com/getkin/kin-openapi/routers/legacy"
)

// LegacyStrictServer lets the generated strict router own request binding and
// route registration while established handlers are migrated operation by operation.
// The established handler still writes the response so the cutover is wire-compatible.
type LegacyStrictServer struct {
\tHandler http.Handler
\tRouter  routers.Router
}

var _ StrictServerInterface = (*LegacyStrictServer)(nil)

type legacyRequestContextKey struct{}

const maxLegacyRequestBody = 64 << 20

type legacyRequest struct {
\trequest *http.Request
\tbody    []byte
}

type legacyCaptureMux struct {
\tServeMux
}

func (mux legacyCaptureMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
\tmux.ServeMux.HandleFunc(pattern, captureLegacyRequest(http.HandlerFunc(handler)).ServeHTTP)
}

// RegisterLegacyStrictHandler registers every generated operation on mux.
func RegisterLegacyStrictHandler(handler http.Handler, mux ServeMux, middlewares ...StrictMiddlewareFunc) {
\tserver, err := newLegacyStrictServer(handler)
\tif err != nil {
\t\tpanic(err)
\t}
\tstrict := NewStrictHandler(server, middlewares)
\tHandlerFromMux(strict, legacyCaptureMux{ServeMux: mux})
}

func newLegacyStrictServer(handler http.Handler) (*LegacyStrictServer, error) {
\tspec, err := GetSwagger()
\tif err != nil {
\t\treturn nil, fmt.Errorf("load generated OpenAPI contract: %w", err)
\t}
\t// Contract server URLs describe clients' deployment targets. An inbound
\t// server must accept the same operation behind any configured host/proxy.
\tspec.Servers = nil
\trouter, err := legacyrouter.NewRouter(spec)
\tif err != nil {
\t\treturn nil, fmt.Errorf("build generated OpenAPI response router: %w", err)
\t}
\treturn &LegacyStrictServer{Handler: handler, Router: router}, nil
}

func captureLegacyRequest(next http.Handler) http.Handler {
\treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
\t\tvar body []byte
\t\tif r.Body != nil {
\t\t\tvar err error
\t\t\tbody, err = io.ReadAll(io.LimitReader(r.Body, maxLegacyRequestBody+1))
\t\t\tif err != nil {
\t\t\t\thttp.Error(w, fmt.Sprintf("read request body: %v", err), http.StatusBadRequest)
\t\t\t\treturn
\t\t\t}
\t\t\t_ = r.Body.Close()
\t\t\tif len(body) > maxLegacyRequestBody {
\t\t\t\thttp.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
\t\t\t\treturn
\t\t\t}
\t\t}
\t\tr.Body = io.NopCloser(bytes.NewReader(body))
\t\tcaptured := &legacyRequest{body: body}
\t\tctx := context.WithValue(r.Context(), legacyRequestContextKey{}, captured)
\t\tcaptured.request = r.WithContext(ctx)
\t\tnext.ServeHTTP(w, captured.request)
\t})
}

func legacyRequestFromContext(ctx context.Context) (*legacyRequest, error) {
\trequest, ok := ctx.Value(legacyRequestContextKey{}).(*legacyRequest)
\tif !ok || request == nil || request.request == nil {
\t\treturn nil, fmt.Errorf("generated OpenAPI handler called without request capture")
\t}
\treturn request, nil
}

func (server *LegacyStrictServer) serveLegacy(w http.ResponseWriter, request *legacyRequest) error {
\tif server == nil || server.Handler == nil {
\t\treturn fmt.Errorf("generated OpenAPI handler has no legacy delegate")
\t}
\tif server.Router == nil {
\t\treturn fmt.Errorf("generated OpenAPI handler has no response validator")
\t}
\trequest.request.Body = io.NopCloser(bytes.NewReader(request.body))
\trecorder := httptest.NewRecorder()
\tserver.Handler.ServeHTTP(recorder, request.request)

\troute, pathParams, err := server.Router.FindRoute(request.request)
\tif err != nil {
\t\treturn fmt.Errorf("match generated OpenAPI response route: %w", err)
\t}
\tvalidation := &openapi3filter.ResponseValidationInput{
\t\tRequestValidationInput: &openapi3filter.RequestValidationInput{
\t\t\tRequest:    request.request,
\t\t\tPathParams: pathParams,
\t\t\tRoute:      route,
\t\t},
\t\tStatus: recorder.Code,
\t\tHeader: recorder.Header().Clone(),
\t\tOptions: &openapi3filter.Options{
\t\t\tIncludeResponseStatus: true,
\t\t},
\t}
\tvalidation.SetBodyBytes(recorder.Body.Bytes())
\tif err := openapi3filter.ValidateResponse(request.request.Context(), validation); err != nil {
\t\treturn fmt.Errorf("legacy response violates generated OpenAPI contract: %w", err)
\t}

\tfor name, values := range recorder.Header() {
\t\tw.Header()[name] = append([]string(nil), values...)
\t}
\tw.WriteHeader(recorder.Code)
\tif _, err := w.Write(recorder.Body.Bytes()); err != nil {
\t\treturn fmt.Errorf("write validated legacy response: %w", err)
\t}
\treturn nil
}
${responseTypes}
${implementations}
`;
  fs.writeFileSync(path.join(root, locations.bridge), generated, 'utf8');
  run('gofmt', ['-w', locations.bridge]);

  const generatedTest = `// Code generated by tools/openapi/contracts.mjs; DO NOT EDIT.

package ${packageName}

import (
\t"net/http"
\t"net/http/httptest"
\t"strings"
\t"testing"
)

func TestLegacyStrictServerRejectsContractViolationForEveryJSONSuccess(t *testing.T) {
\ttests := []struct {
\t\tname   string
\t\tmethod string
\t\tpath   string
\t\tstatus int
\t}{
${responseProbeCases}
\t}
\tfor _, test := range tests {
\t\tt.Run(test.name, func(t *testing.T) {
\t\t\tserver, err := newLegacyStrictServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
\t\t\t\tw.Header().Set("Content-Type", "application/json")
\t\t\t\tw.WriteHeader(test.status)
\t\t\t\t_, _ = w.Write([]byte("null"))
\t\t\t}))
\t\t\tif err != nil {
\t\t\t\tt.Fatalf("newLegacyStrictServer() error = %v", err)
\t\t\t}
\t\t\trequest := httptest.NewRequest(test.method, test.path, nil)
\t\t\tresponse := httptest.NewRecorder()

\t\t\terr = server.serveLegacy(response, &legacyRequest{request: request})
\t\t\tif err == nil || !strings.Contains(err.Error(), "violates generated OpenAPI contract") {
\t\t\t\tt.Fatalf("serveLegacy() error = %v, want contract violation", err)
\t\t\t}
\t\t\tif response.Body.Len() != 0 || response.Header().Get("Content-Type") != "" {
\t\t\t\tt.Fatalf("invalid response leaked to client: headers = %v, body = %q", response.Header(), response.Body.String())
\t\t\t}
\t\t})
\t}
}

func TestLegacyStrictServerPassesValidDefaultResponse(t *testing.T) {
\tserver, err := newLegacyStrictServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
\t\tw.Header().Set("Content-Type", "text/plain")
\t\tw.WriteHeader(599)
\t\t_, _ = w.Write([]byte("legacy error"))
\t}))
\tif err != nil {
\t\tt.Fatalf("newLegacyStrictServer() error = %v", err)
\t}
\trequest := httptest.NewRequest("${defaultResponseProbe.method}", "${defaultResponseProbe.path}", nil)
\tresponse := httptest.NewRecorder()

\tif err := server.serveLegacy(response, &legacyRequest{request: request}); err != nil {
\t\tt.Fatalf("serveLegacy() error = %v", err)
\t}
\tif response.Code != 599 || response.Header().Get("Content-Type") != "text/plain" || response.Body.String() != "legacy error" {
\t\tt.Fatalf("validated response = (%d, %q, %q), want (599, text/plain, legacy error)", response.Code, response.Header().Get("Content-Type"), response.Body.String())
\t}
}

func TestLegacyStrictServerPassesValidJSONDefaultResponse(t *testing.T) {
\tserver, err := newLegacyStrictServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
\t\tw.Header().Set("Content-Type", "application/json")
\t\tw.WriteHeader(599)
\t\t_, _ = w.Write([]byte("{}"))
\t}))
\tif err != nil {
\t\tt.Fatalf("newLegacyStrictServer() error = %v", err)
\t}
\trequest := httptest.NewRequest("${defaultResponseProbe.method}", "${defaultResponseProbe.path}", nil)
\tresponse := httptest.NewRecorder()

\tif err := server.serveLegacy(response, &legacyRequest{request: request}); err != nil {
\t\tt.Fatalf("serveLegacy() error = %v", err)
\t}
\tif response.Code != 599 || response.Header().Get("Content-Type") != "application/json" || response.Body.String() != "{}" {
\t\tt.Fatalf("validated response = (%d, %q, %q), want (599, application/json, {})", response.Code, response.Header().Get("Content-Type"), response.Body.String())
\t}
}
`;
  fs.writeFileSync(path.join(root, locations.bridgeTest), generatedTest, 'utf8');
  run('gofmt', ['-w', locations.bridgeTest]);
}

function generateGo() {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'dexarb-openapi-'));
  try {
    for (const contract of manifest.contracts) {
      const locations = generatedLocations(contract);
      const packageName = contract.id === 'bisqstats-public' ? 'bisqstatsapi' : generatedPackage[contract.audience];
      const configs = [
        {
          name: 'server',
          output: locations.server,
          package: packageName,
          generate: {
            models: true,
            'std-http-server': true,
            'strict-server': true,
            'embedded-spec': true,
          },
        },
        {
          name: 'client',
          output: locations.client,
          package: contract.id.replaceAll('-', ''),
          generate: { models: true, client: true },
        },
      ];
      for (const config of configs) {
        fs.mkdirSync(path.dirname(path.join(root, config.output)), { recursive: true });
        const configPath = path.join(tempDir, `${contract.id}.${config.name}.yaml`);
        fs.writeFileSync(configPath, stringify({
          package: config.package,
          generate: config.generate,
          output: path.join(root, config.output),
          'output-options': { 'exclude-tags': ['raw'] },
        }), 'utf8');
        run('go', [
          '-C', path.join(root, 'tools/openapi'),
          'tool', 'oapi-codegen',
          '--config', configPath,
          path.join(root, contract.bundle),
        ]);
      }
      generateLegacyBridge(contract, locations, packageName);
    }
  } finally {
    fs.rmSync(tempDir, { recursive: true, force: true });
  }
}

function generate() {
  bundle();
  generateGo();
  run('npm', ['--prefix', 'dexMonitor/react-frontend', 'run', 'generate:api']);
}

const routeSources = {
  dexUnifiedApi: ['dexUnifiedApi/cmd/server/routes.go'],
  dexMmController: ['dexMmController/internal/api/server.go'],
  dexMonitor: [
    'dexMonitor/cmd/server/routes.go',
    'dexMonitor/internal/auth/auth.go',
    'dexMonitor/cmd/bisqstats/main.go',
  ],
  dexFiatController: ['dexFiatController/internal/pushpush/routes.go'],
};

function specOperations(service) {
  const generated = new Set();
  const raw = new Set();
  for (const contract of manifest.contracts.filter((entry) => entry.service === service)) {
    const spec = parse(fs.readFileSync(path.join(root, contract.source), 'utf8'));
    for (const [route, pathItem] of Object.entries(spec.paths ?? {})) {
      for (const [method, operation] of Object.entries(pathItem)) {
        if (['get', 'put', 'post', 'delete', 'patch', 'head', 'options', 'trace'].includes(method)) {
          const target = operation.tags?.includes('raw') ? raw : generated;
          target.add(`${method.toUpperCase()} ${route}`);
        }
      }
    }
  }
  return { generated, raw, all: new Set([...generated, ...raw]) };
}

function registeredOperations(service) {
  const operations = new Set();
  const matcher = /(?:HandleFunc|Handle)\("([A-Z]+) ([^" ]+)"/g;
  for (const source of routeSources[service]) {
    const content = fs.readFileSync(path.join(root, source), 'utf8');
    for (const match of content.matchAll(matcher)) {
      if (match[2].includes('...}') || match[2] === '/metrics') continue;
      operations.add(`${match[1]} ${match[2]}`);
    }
  }
  return operations;
}

function generatedOperations(service) {
  const operations = new Set();
  for (const contract of manifest.contracts.filter((entry) => entry.service === service)) {
    const source = fs.readFileSync(path.join(root, generatedLocations(contract).server), 'utf8');
    const matcher = /m\.HandleFunc\(http\.Method(\w+)\+" "\+options\.BaseURL\+"([^"]+)"/g;
    for (const match of source.matchAll(matcher)) {
      operations.add(`${match[1].toUpperCase()} ${match[2]}`);
    }
  }
  return operations;
}

function reportSetDifference(service, label, expected, actual) {
  const missing = [...expected].filter((route) => !actual.has(route)).sort();
  const unexpected = [...actual].filter((route) => !expected.has(route)).sort();
  if (!missing.length && !unexpected.length) return false;
  process.stderr.write(`${service}: ${label} mismatch\n`);
  for (const route of missing) process.stderr.write(`  missing: ${route}\n`);
  for (const route of unexpected) process.stderr.write(`  unexpected: ${route}\n`);
  return true;
}

function routeCheck() {
  let failed = false;
  for (const service of Object.keys(routeSources)) {
    const expected = specOperations(service);
    const registered = registeredOperations(service);
    const generated = generatedOperations(service);
    const operational = new Set((manifest.operationalRoutes[service] ?? []).filter((route) => !route.startsWith('* ')));
    const contractRegistrations = new Set([...registered].filter((route) => !operational.has(route)));

    failed = reportSetDifference(service, 'contract-to-handler coverage', expected.all, contractRegistrations) || failed;
    failed = reportSetDifference(service, 'generated route ownership', expected.generated, generated) || failed;
    const missingRaw = [...expected.raw].filter((route) => !registered.has(route)).sort();
    if (missingRaw.length) {
      failed = true;
      process.stderr.write(`${service}: raw route registration mismatch\n`);
      for (const route of missingRaw) process.stderr.write(`  missing handwritten route: ${route}\n`);
    }
    if (!failed) {
      process.stdout.write(`${service}: ${expected.generated.size} generated, ${expected.raw.size} raw operations accounted for\n`);
    }
  }
  if (failed) process.exit(1);
}

function generatedFiles() {
  const files = new Set([
    'dexMonitor/react-frontend/src/generated/monitor-api.ts',
    'dexMonitor/react-frontend/src/generated/unified-api.ts',
  ]);
  for (const contract of manifest.contracts) {
    const locations = generatedLocations(contract);
    files.add(contract.bundle);
    files.add(locations.server);
    files.add(locations.bridge);
    files.add(locations.bridgeTest);
    files.add(locations.client);
  }
  return [...files].sort();
}

function generatedSnapshot() {
  return new Map(generatedFiles().map((file) => {
    const absolute = path.join(root, file);
    const digest = fs.existsSync(absolute)
      ? createHash('sha256').update(fs.readFileSync(absolute)).digest('hex')
      : '<missing>';
    return [file, digest];
  }));
}

function generatedStatus(before) {
  const after = generatedSnapshot();
  const changed = [...after].filter(([file, digest]) => before.get(file) !== digest).map(([file]) => file);
  if (changed.length) {
    process.stderr.write(`generated artifacts are stale:\n${changed.map((file) => `  ${file}`).join('\n')}\n`);
    process.exit(1);
  }
}

const command = process.argv[2];
switch (command) {
  case 'bundle': bundle(); break;
  case 'lint': lint(); break;
  case 'generate': generate(); break;
  case 'route-check': routeCheck(); break;
  case 'check': {
    const before = generatedSnapshot();
    lint();
    generateGo();
    run('npm', ['--prefix', 'dexMonitor/react-frontend', 'run', 'generate:api']);
    routeCheck();
    generatedStatus(before);
    break;
  }
  default:
    process.stderr.write('usage: node tools/openapi/contracts.mjs bundle|lint|generate|route-check|check\n');
    process.exit(2);
}
