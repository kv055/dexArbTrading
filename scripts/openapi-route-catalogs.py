#!/usr/bin/env python3
"""Generate deterministic OpenAPI route catalogs from Go ServeMux registrations.

These catalogs document the complete HTTP surface. Detailed request/response
contracts remain service-owned files and can replace catalog operations as the
handlers are formalized.
"""

from __future__ import annotations

import argparse
import difflib
import itertools
import re
from dataclasses import dataclass
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ROUTE_RE = re.compile(r'(?:HandleFunc|Handle)\("([A-Z]+) ([^" ]+)"')


@dataclass(frozen=True)
class Service:
    title: str
    output: str
    sources: tuple[str, ...]
    server_url: str
    description: str


SERVICES = (
    Service(
        "dexUnifiedApi",
        "dexUnifiedApi/api/openapi.yaml",
        ("dexUnifiedApi/cmd/server/routes.go",),
        "http://localhost:5010",
        "Unified offer, notification, configuration, and internal ingestion routes.",
    ),
    Service(
        "dexMmController",
        "dexMmController/api/openapi.yaml",
        ("dexMmController/internal/api/server.go",),
        "http://localhost:5001",
        "Market-making, CEX, Lightning, Bisq, Telegram, and transaction routes.",
    ),
    Service(
        "dexMonitor",
        "dexMonitor/api/openapi.yaml",
        (
            "dexMonitor/cmd/server/routes.go",
            "dexMonitor/internal/auth/auth.go",
        ),
        "http://localhost:5002",
        "Dashboard analytics, configuration, authentication, and controller proxy routes.",
    ),
    Service(
        "dexMonitor bisqstats",
        "dexMonitor/api/bisqstats.openapi.yaml",
        ("dexMonitor/cmd/bisqstats/main.go",),
        "http://localhost:9997",
        "Standalone Bisq closed-trade statistics command.",
    ),
    Service(
        "dexFiatController",
        "dexFiatController/api/openapi.yaml",
        ("dexFiatController/internal/pushpush/routes.go",),
        "http://localhost:8080",
        "Payment-notification ingestion, heartbeat, health, and webhook routes.",
    ),
)


def routes_for(service: Service) -> list[tuple[str, str]]:
    routes: set[tuple[str, str]] = set()
    for relative in service.sources:
        text = (ROOT / relative).read_text(encoding="utf-8")
        for method, path in ROUTE_RE.findall(text):
            # Capture-only wildcard handlers are diagnostics, not a stable API.
            if "...}" not in path and path != "/metrics":
                routes.add((path, method.lower()))
    return sorted(routes)


def yaml_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def operation_id(method: str, path: str) -> str:
    words = re.findall(r"[A-Za-z0-9]+", path)
    suffix = "".join(word[:1].upper() + word[1:] for word in words)
    return method.lower() + suffix


def tag_for(path: str) -> str:
    parts = [part for part in path.split("/") if part and not part.startswith("{")]
    if not parts:
        return "service"
    if parts[0] in {"api", "internal", "ws"} and len(parts) > 1:
        return parts[1].replace("_", "-")
    return parts[0].replace("_", "-")


def render(service: Service) -> str:
    routes = routes_for(service)
    lines = [
        "openapi: 3.1.0",
        "info:",
        f"  title: {yaml_quote(service.title + ' HTTP API')}",
        "  version: 0.1.0",
        "  description: |",
        f"    {service.description}",
        "    This file is a generated, complete route catalog. Operations marked",
        "    with `x-contract-status: route-catalog` do not yet claim fully typed",
        "    request and response schemas; consult the handler until formalized.",
        "servers:",
        f"  - url: {yaml_quote(service.server_url)}",
        "paths:",
    ]
    for path, path_routes in itertools.groupby(routes, key=lambda route: route[0]):
        lines.append(f"  {yaml_quote(path)}:")
        for _, method in path_routes:
            lines.append(f"    {method}:")
            lines.append(f"      operationId: {operation_id(method, path)}")
            lines.append(f"      tags: [{yaml_quote(tag_for(path))}]")
            lines.append("      summary: " + yaml_quote(f"{method.upper()} {path}"))
            lines.append("      x-contract-status: route-catalog")
            parameters = re.findall(r"{([^}]+)}", path)
            if parameters:
                lines.append("      parameters:")
            for parameter in parameters:
                lines.extend(
                    (
                        f"        - name: {yaml_quote(parameter)}",
                        "          in: path",
                        "          required: true",
                        "          schema:",
                        "            type: string",
                    )
                )
            lines.append("      responses:")
            if path.startswith("/ws/") or "/ws/" in path:
                lines.append("        '101':")
                lines.append("          description: WebSocket protocol upgrade")
            else:
                lines.append("        '200':")
                lines.append("          description: Successful response")
            lines.append("        default:")
            lines.append("          description: Error response")
    if not routes:
        lines.append("  {}")
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true", help="fail if catalogs are stale")
    args = parser.parse_args()
    stale = False
    for service in SERVICES:
        output = ROOT / service.output
        expected = render(service)
        actual = output.read_text(encoding="utf-8") if output.exists() else ""
        if args.check:
            if actual != expected:
                stale = True
                print(f"stale OpenAPI route catalog: {service.output}")
                print("".join(difflib.unified_diff(actual.splitlines(True), expected.splitlines(True), n=2)))
        else:
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_text(expected, encoding="utf-8")
            print(f"wrote {service.output} ({len(routes_for(service))} operations)")
    return 1 if stale else 0


if __name__ == "__main__":
    raise SystemExit(main())
