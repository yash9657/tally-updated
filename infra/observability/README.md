# infra/observability — telemetry baseline

`PLAN.md` A.4 mandates OpenTelemetry traces/metrics/logs everywhere, exported
to **Grafana / Tempo / Loki / Mimir** with Sentry for client errors.

## Files

- `otel-collector-config.yaml` — OpenTelemetry Collector skeleton. Receives
  OTLP (gRPC 4317 / HTTP 4318) from the three planes; pipelines fan out to the
  backends below. Endpoints are placeholders resolved per environment.

## Backend topology (the LGTM-style stack)

| Signal | Backend | Why |
|---|---|---|
| Traces | **Tempo** | Distributed traces across `cored` → `statutoryd`/`insightd`; the canonical product SLO is **voucher-post p99 latency** (keystroke feel). |
| Metrics | **Prometheus / Mimir** | Golden-signal SLOs + error budgets; dedicated dashboards on government-API latency, IRN/filing rejection rates, and GSP token-refresh health (the externalities most likely to cascade). |
| Logs | **Loki** | Structured logs correlated to traces by `trace_id`. |
| Dashboards | **Grafana** | Single pane over Tempo/Loki/Mimir. |
| Client errors | **Sentry** | Frontend/PWA error tracking. |

All telemetry stays inside **ap-south-1** (the collector stamps
`cloud.region: ap-south-1`) to respect India data residency — only redacted
Claude payloads ever cross the border, never telemetry.

## How apps wire in

Each plane sets `OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.observability.svc:4317`
and `OTEL_SERVICE_NAME=<plane>` (see the env blocks in `infra/k8s/base/*.yaml`).
The collector is deployed (Helm/Argo CD) into an `observability` namespace; this
repo ships the **config** skeleton, not the deployment chart (that lands with
the Argo CD app-of-apps in a later phase).
