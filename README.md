# generation-context

`generation-context` defines the public, invocation-scoped context shared by OpenAPI generation callers and the OpenAPI generator.

The `access` package distinguishes explicit direct generation from authenticated generation and carries validated access metadata plus the caller-decided generated-output license. The `telemetry` package owns active event pointer identity and panic-safe lifecycle orchestration, with a default lifecycle that finalizes real events without persistence or network delivery.

This module does not collect authentication, API-client, entitlement, telemetry-delivery, credential, endpoint, host/process/Git, or customer data. Callers may supply invocation metadata through its public types; delivery adapters remain responsible for redaction, opt-out enforcement, and persistence.

This module is licensed under the GNU Affero General Public License v3.0; see [`LICENSE`](LICENSE).
