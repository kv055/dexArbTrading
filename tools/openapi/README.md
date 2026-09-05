# Maintaining API contracts

Update the handwritten service contract when an HTTP response changes, then run
`make api-generate`. Reusable wire types belong in `commons/api/openapi/`; the
public and upstream CEX list endpoints reference the same field schemas.
Frontend CEX list helpers use `ApiJson` from the generated monitor client
instead of handwritten item types.

Run `make api-check` and `make api-contract-test` before shipping:

- The first checks syntax, generation, registered routes, and successful response
  contracts for dexMonitor's MM-controller and CEX proxy namespaces. This includes
  raw proxies. A missing upstream operation or differing documented response fails.
- The second runs response tests across every service and stops on any module
  failure. Generated rejection tests prove validation is enabled; they do not
  prove that a handler's successful response matches its contract.

For changed handlers, add positive tests through the registered generated router
using a test database and deterministic upstream services. Exercise populated and
empty lists, required fields, and upstream failures. Calling a legacy handler
directly bypasses response validation.

The regression tests in dexMonitor exercise the actual analytics handlers and
HTTP proxy through validation. The MM-controller test exercises LSP channel
candidates through its registered handler. The JavaScript tests deliberately
restore the original deposit-address schema error and assert that CI rejects it.

The proxy comparison currently covers explicitly documented successful responses
in those two namespaces. It does not establish request-parameter completeness,
timeout behavior, or field correctness where both schemas are equally permissive.
Extend real-handler coverage and replace generic object schemas incrementally.
Spot-trade listing currently returns an unsupported-operation error; its shared
success schema describes the existing wire type, not a newly enabled capability.
