<p align="center">
  <a href="https://tallyweb.world/">
    <img src="frontend/public/brand/tallyweb-logo.svg" alt="Tallyweb" width="344" />
  </a>
</p>

<h1 align="center">Familiar keys. A new chapter.</h1>

<p align="center">Keyboard-first accounting, inventory, and AI-assisted workflows — in your browser.</p>

<p align="center">
  <a href="https://tallyweb.world/">Website</a> ·
  <a href="https://tallyweb.world/signin">Sign in</a> ·
  <a href="#getting-started">Getting started</a> ·
  <a href="DESIGN.md">Design system</a>
</p>

## What is Tallyweb?

Tallyweb is an accounting and ERP application for Indian businesses and their accountants. It combines familiar Tally-style keyboard navigation with a browser-based workspace and tools that help turn business documents into reviewable accounting work.

- **Accounting:** sales, purchases, receipts, payments, ledgers, and versioned vouchers.
- **Reports:** Day Book, Balance Sheet, Profit & Loss, outstanding balances, and drill-down reports.
- **Inventory:** stock summaries, stock movements, sales and purchase orders, and bills of materials.
- **Banking and tax workflows:** bank statement imports, reconciliation, GST reports, and TDS workflows.
- **AI assistance:** bill-to-draft processing, questions about your books, insights, and an Approval Inbox for agent proposals.
- **Business controls:** company-scoped access, roles and permissions, voucher edit history, and an offline outbox for queued entries.

Capabilities depend on service configuration. AI providers, OCR, and statutory integrations require their own setup; the repository does not imply live filing or production certification. See the [deployment guide](docs/DEPLOY_VERCEL.md) and [service documentation](insightd/README.md) for limitations.

## Getting started

### Frontend preview

Use Node.js 22+ and pnpm:

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm dev
```

Open [localhost:5173](http://localhost:5173) for the landing page. `/signin` contains the sign-in form; `/app` is the authenticated workspace. The public landing page needs no backend. Sign-in and workspace operations use the same-origin `/auth` and `/v1` APIs, proxied to `localhost:8087` by Vite. Override that target with `CORED_PROXY_TARGET` when needed.

The browser entry always requires authentication for the workspace. The standalone `App` component retains its in-memory composition for isolated tests and development harnesses.

### Full development stack

Also install Go 1.26.4+, Python 3.11+, and Docker. From the repository root:

```bash
make dev-db
make migrate
python3 -m venv insightd/.venv
insightd/.venv/bin/pip install -r insightd/requirements.txt -r insightd/requirements-db.txt
```

Run each service in a separate terminal:

```bash
make run-cored       # API and accounting core on :8087
make run-insightd    # Document-to-draft service on :8088
make run-statutoryd  # Optional statutory service on :8091; start cored first
```

Then start the frontend with `cd frontend && pnpm dev`. Use `make seed-all` to prepare the local demo data and development identities; see `make help` for seeding and verification targets.

Use the Makefile service targets: they supply the local database settings and shared service credentials. Restart `run-statutoryd` after restarting cored because its development service token is tied to cored’s signing key. These local development defaults are not production credentials.

## Repository structure

```text
frontend/     React, TypeScript, XState, and Vite application
backend/      Go accounting core, auth, APIs, migrations, and statutory service
insightd/     Python/FastAPI document-to-draft service
corpora/      Golden datasets and cross-service contracts
infra/        Infrastructure configuration and operational guides
docs/         Architecture, deployment, research, and archived reference material
```

The browser talks to **cored**. Cored authenticates requests, checks company membership and permissions, and proxies approved requests to the document and statutory services. The document service produces drafts; it does not write directly to the ledger.

The ledger records voucher changes as new versions. Double-entry constraints, tenant isolation, and keyboard behavior are covered by the repository’s verification suites. Read [Tenancy and Access](docs/TENANCY-AND-ACCESS.md) for the security model.

## Design and branding

The [design system](DESIGN.md) documents reusable tokens, components, accessibility, and the relationship between the public pages and accounting workspace.

- `frontend/src/design-system/tokens.css` — shared colors, typography, spacing, shapes, motion, and focus tokens.
- `frontend/src/design-system/components.tsx` — `Brand`, `Button`, `ButtonLink`, `Keycap`, and `ArrowIcon`.
- `frontend/tailwind.config.ts` — existing `tally-*` utilities mapped to the same CSS variables, including opacity modifiers.
- `frontend/public/brand/` — SVG logo and mark; `frontend/public/favicon.svg` is the browser icon.

IBM Plex Sans is self-hosted under the [SIL Open Font License](frontend/public/fonts/OFL.txt). Financial tables and keyboard controls retain the monospace typography of the workspace.

## Verification

```bash
cd frontend
pnpm build          # TypeScript check and production bundle
pnpm test:run       # Frontend unit and integration suites
```

CI uses Node.js 22. On Node.js 26, run tests with
`NODE_OPTIONS=--no-experimental-webstorage pnpm test:run` so Node's experimental
Web Storage does not shadow jsdom's browser storage.

From the repository root:

```bash
make backend-test   # Requires the local database and migrations
make corpora-verify
make insightd-test
make verify         # Full configured verification gates
```

`pnpm e2e:ui` in `frontend/` runs the browser smoke suite against a seeded local stack. Its default entry is `/signin`.

## Documentation

- [Deployment](docs/DEPLOY_VERCEL.md)
- [Tenancy and access](docs/TENANCY-AND-ACCESS.md)
- [Architecture and feature design notes](docs/design/)
- [Golden datasets](corpora/README.md)
- [Historical runbook and defect register](docs/archive/)

Tallyweb is an independent project. Tally and TallyPrime are trademarks of their respective owner. This project is not affiliated with or endorsed by Tally Solutions.
