// Package mcp is cored's Model Context Protocol server (PLAN-AGENTIC §3 W6,
// D126): JSON-RPC 2.0 over the Streamable HTTP transport, hand-rolled on the
// standard library because IMPLEMENTATION_PLAN §1.1 admits no new module — the
// same reason internal/trajectory's OTLP exporter is hand-written.
//
// # WHAT AN MCP CLIENT IS HERE
//
// An agent principal acting for ONE human under a standing grant that human
// made (0105's agent_mcp_grant): ACL keys, a paise ceiling, an expiry. It
// authenticates with that human's own bearer token through internal/authn and
// the router's companyAuthMiddleware — there is no second identity system,
// and a grant is usable only with its own user's token, in its own company.
//
// # ⛔ THE SAME PATH AS THE IN-APP AGENT, BY CONSTRUCTION
//
// initialize starts an agent run (agent.Engine.StartExternal); every
// tools/call becomes one agent.ModelDecision handed to agent.Engine.Submit,
// which records it as the run's decide:k and lets the agent loop derive the
// rest: actions.read / actions.preview through the dispatcher (the real
// router, in process, as the human), actions.commit through the policy gate,
// the approval inbox and the idempotent commit. This package holds no
// agent.Dispatcher, builds no agent.Request and imports no feature package;
// TestMCPReachesNoHandlerExceptThroughTheAgent pins that from the source, and
// internal/archtest pins the import set.
//
// Consequences a client sees:
//   - tools/list is generated from internal/actions, filtered by
//     policy.EffectiveRights for the grant — reads, previews of the delegated
//     previewable writes, and commit_preview.
//   - every MCP run is born with policy.SourceMCPClient in its context, so a
//     money, master or statutory commit ALWAYS waits for a human, whatever the
//     tenant's autonomy table says: the result is "pending_approval" with the
//     approval id, resolved only in the product's approval inbox.
//   - the manifest's SHA-256 is pinned per session at initialize; a changed
//     registry or grant refuses every later call with CodeManifestChanged.
//
// # PROTOCOL SCOPE
//
// Versions 2025-11-25 and 2025-06-18 (both without JSON-RPC batching). The
// tools capability only (listChanged false); no resources, prompts, sampling,
// elicitation, logging or tasks; no server-initiated SSE stream (GET is 405).
// OAuth 2.1 authorization-server metadata, Protected Resource Metadata (RFC
// 9728) and Client ID Metadata Documents are NOT implemented — D126 §9.
package mcp
