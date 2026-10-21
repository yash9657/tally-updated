# Fireworks agent: local operation and evaluation

The agent runs through cored's existing model adapter, tenant permissions,
preview/approval/commit engine, and immutable intent log. It supports the
bookkeeping actions exposed by that engine. It is not yet an unrestricted
computer-use or general-purpose business agent.

## Run locally

Use Node 22 for the frontend (the same major version as CI), Go 1.26 for cored,
and PostgreSQL 16 with pgvector. Apply the migrations with the administrator DSN;
run the server with the least-privilege `tally_app` DSN. Use a separate development
database, not a production company or a concurrently running test database.

Set these server environment variables using your secret manager or local shell:

```text
LLM_PROVIDER=fireworks
LLM_MODEL=accounts/fireworks/models/deepseek-v4p1-flash
LLM_API_KEY=<your Fireworks key>
LLM_TIMEOUT=90s
LLM_BUDGET_TOKENS_PER_MONTH=100000
APP_DSN=<development tally_app PostgreSQL URL>
PORT=8089
```

`FIREWORKS_API_KEY` alone configures the evaluation runner; cored itself expects
`LLM_API_KEY`. Do not put either key in a `VITE_` variable or commit it.

Run `go run ./cmd/cored serve` from `backend`. Run the frontend with
`CORED_PROXY_TARGET=http://localhost:8089 VITE_LIVE=1 pnpm dev` from `frontend`.
Sign in, select a company, press Ctrl+K, and select the write permissions for the
run. A run without delegated writes is read-only. Proposed writes still require
approval under the company's autonomy policy. The monthly token limit is shared
by the agent and natural-language query translator for each tenant; it is not a
provider billing-account dollar cap.

## Paid, synthetic evaluation

Normal CI never contacts Fireworks. The live suite requires the `livefireworks`
build tag and explicit spending inputs. It creates a new database, applies the
real migrations, creates synthetic companies, and drops the database afterwards.
The administrator account must be able to create/drop databases and manage the
application roles. Use a private local PostgreSQL instance.

```sh
export ADMIN_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55439/tally_verify?sslmode=disable'
python3 scripts/evals/fireworks.py \
  --env-file /absolute/path/to/private.env.local \
  --model accounts/fireworks/models/deepseek-v4p1-flash \
  --max-usd 2.50 --token-price-per-million-usd 1 \
  --report /tmp/fireworks-evaluation.json
```

For a small check, add `--tasks guard-001-en,ve-receipt-000-en` and lower the cap.
The default selects one task per family/language plus all ten guards: 28 tasks.
Every trial uses a fresh company. The model sees the user instruction and real
tool results, never the reference plan or expected state. The fixture operator
explicitly delegates the supported writes and approves non-guard proposals via
the normal API; guards receive no approvals. A guard state pass alone does not
prove that the model refused: inspect `AgentErr` and the answer as well.

The transport reserves `(encoded request bytes + max_tokens + 4096) × rate`
before each provider call, including translator repairs. Failed requests keep
their reservation. It refuses any other destination, unbounded completion,
more than 150 calls, or a reservation above the cap. This deliberately overstates
cost; the report also records usage, latency, provider model and response IDs.
The ceiling is **per invocation**: subtract previous reservations from a shared
budget before rerunning. Verify current model availability and pricing first.

Reports contain synthetic prompts, tool decisions and task-level state/answer
verdicts. They are mode 0600 and contain no authentication headers. Any task
failure produces a nonzero exit; do not advertise a partial run as a clean gate.
The money judge excludes complete UUIDs but still rejects missing figures and
unrelated money dumps. Reference/null/wrong-agent controls remain in normal CI.

## Current provider references

Verified on 21 October 2026: the configured account serves DeepSeek V4.1 Flash.
Published rates are $0.22/M input, $0.007/M cached input and $0.66/M output;
$1/M is the conservative rate used above. Recheck before another run:
[model pricing](https://fireworks.ai/models/deepseek-ai/deepseek-v4p1-flash),
[function calling](https://docs.fireworks.ai/guides/function-calling),
[reasoning token controls](https://docs.fireworks.ai/guides/reasoning).
