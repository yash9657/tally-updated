# R7 — India-specific channels, data rails, and regulatory constraints for an agentic SMB finance product

Date: 2026-09-08. Research method: web search (6 queries before the session search budget was exhausted) plus ~90 direct fetches of primary sources (Meta developer docs, Sarvam docs, Sahamati, GSTN, CERT-In, RBI, Setu, AI4Bharat/HF model cards, BSP pricing pages, etc.). Many Indian government/bank portals are JS shells or geo-fenced and returned no text; where a fact could not be verified in this session it is marked **[unverified — prior knowledge]**. Everything else is traceable to the Sources list.

---

## 1. WhatsApp Business Platform (Meta Cloud API) — and SMS/DLT, RCS, Telegram

### 1.1 Pricing model (verified against Meta docs + Jan-2026 India rate card)

- **Per-message billing since 1 July 2025.** Meta charges "only when a template message is delivered"; the old 24-hour conversation bundle is gone. Category and recipient country code set the rate. (developers.facebook.com/docs/whatsapp/pricing)
- **Free**: all non-template (free-form) replies inside the **24-hour customer service window (CSW)** opened by a user message; **utility templates sent inside an open CSW**; and **everything inside the 72-hour Free Entry Point window** after a Click-to-WhatsApp ad / Page CTA click. Service conversations are free.
- **Volume tiers** apply only to utility + authentication, aggregated across all WABAs in a business portfolio, market-specific, reset monthly.
- **India billing localisation**: from 1 Jan 2026 eligible Indian businesses are billed in INR; migration mandatory by 31 Dec 2026, or delivery disruption from 1 Jan 2027. (Meta pricing doc)
- **Marketing Messages API** now lets a business set a per-message *maximum price*; Meta charges that or lower.

| India rate (Jan 2026) | Meta list price | AiSensy resale | Interakt resale |
|---|---|---|---|
| Marketing | ₹0.8631 (~$0.010) — no volume discount | ₹1.09 | ₹0.958 |
| Utility | ₹0.115 (~$0.0014); tiers to ₹0.0805 at 100M+/mo | ₹0.145 | ₹0.150 |
| Authentication | ₹0.115; **authentication-international ~$0.0304** (≈22× domestic) | ₹0.145 | ₹0.128 |
| Service (user-initiated, in CSW) | Free | Free | Free |

Sources: blueticks.co 2026 rate card; engagelab; aisensy.com/pricing; interakt.shop/pricing. Note the marketing rate rose ~10% (₹0.7846 → ₹0.8631) with INR localisation.

**What this means for a finance agent**: payment reminders, invoice delivery, statements, OTP-like confirmations are *utility* (₹0.115 list) and become **free whenever the customer has messaged in the last 24h**. A conversational agent that gets the SMB owner to message first (or that lives inside the owner's own WABA talking to the owner) is essentially free; the only material cost is proactive nudges to customers/debtors who have not replied.

### 1.2 Capabilities that matter for bookkeeping

- **Media (Cloud API reference/media)**: images JPEG/PNG ≤5 MB; **documents (PDF/Office) ≤100 MB**; **audio ≤16 MB incl. OGG/Opus voice notes** (mono only); video ≤16 MB. Inbound media is fetched by media-ID → temporary URL that **expires in 5 minutes**; webhook media IDs live **7 days** (uploaded IDs 30 days). Design: download and persist immediately on webhook. Invoice photos and voice notes ("₹4,500 Sharma ji ko diya") are therefore first-class inputs.
- **WhatsApp Flows**: declarative **Flow JSON** screens with TextInput, DatePicker, Dropdown, CheckboxGroup, **photo/document upload**, and an encrypted **data_exchange endpoint** to the business server; triggered by templates (business-initiated) or interactive messages (user-initiated). Use cases Meta cites: lead-gen, appointment booking (Farmacias del Ahorro 2.6× appointments), loan pre-approval, insurance quotes. For us: a "record expense" or "confirm bank match" form inside WhatsApp without an app install.
- **Interactive messages** (reply buttons, list messages), **catalog**, and **WhatsApp Payments (India, UPI)** — Interakt lists "Native WhatsApp Payment integration" and "WhatsApp Forms" on its plans.
- **Business-initiated vs user-initiated**: business-initiated = approved template only (categorised marketing/utility/auth); user-initiated opens 24h CSW for free-form + free utility templates.

### 1.3 BSPs in India (fetched prices)

| BSP | Platform fee | Notes |
|---|---|---|
| **AiSensy** | Chatbot ₹2,500/mo; "WhatsApp AI Agent Builder" ₹1,350/mo (1,000 AI msgs); Indian virtual number ₹2,000/yr | Prepaid credits; markup ~₹0.03/utility msg |
| **Interakt (Haptik/Jio)** | Starter free; Growth/Advanced (~$55/$69/mo); Sales CRM ₹2,499/mo for 5 seats; AI agents add-on $74.99 | Native WhatsApp Payments, Forms, catalog |
| **WATI** | Growth ₹4,999/mo (annual, 3 users, 15k broadcasts), Pro ₹9,999, Business ₹24,999; PAYG ₹999 | Meta rate card passed through |
| Gupshup, Kaleyra, Exotel, Infobip, Twilio | Pricing pages not fetchable this session | Gupshup/Kaleyra/Exotel are the incumbent enterprise BSPs; Exotel also does voice |

Direct Cloud API (no BSP) is possible: Meta Business verification requires PAN/GST certificate, a dedicated number, and template approval (Tally's own WhatsApp-invoicing guide describes exactly this flow). Tally itself ships "WhatsApp for Business in TallyPrime" via a BSP and sends e-invoices, UPI links and QR codes. **[unverified — prior knowledge]**: Tally's WhatsApp add-on is priced as a paid annual pack; Zoho Books, Swipe, Refrens, Vyapar, Khatabook, OkCredit all send invoices/reminders over WhatsApp (OkCredit's site: "3x faster" collection via WhatsApp payment links; Vyapar: bulk WhatsApp reminders).

### 1.4 SMS/DLT, RCS, Telegram

- **TRAI DLT (TCCCPR 2018)**: every sender must register a Principal Entity, headers (sender IDs), content templates and consent templates on a telco DLT platform; messages are "scrubbed" against templates. TRAI's Dec-2024 traceability mandate and the Feb-2025 TCCCPR amendment (140-series for promotional, 1600-series for transactional/service calls, tighter complaint handling) tightened this further **[unverified — pages returned 404/403 this session]**. Practical rule: transactional OTP/statement SMS is fine with pre-registered templates; conversational SMS is a non-starter.
- **RCS Business Messaging (Google)**: JSON API, rich cards/suggested replies, SMS fallback, encryption to Google servers; carrier launch and agent verification per market (docs fetched); India carrier availability/pricing not verified. Treat as SMS+ for reminders, not a chat channel.
- **Telegram**: no India-specific compliance rails; negligible SMB-owner usage vs WhatsApp **[unverified — no survey fetched]**. Skip.

---

## 2. Voice and Indic-language stack

### 2.1 Sarvam AI (verified from docs.sarvam.ai, Sept 2026)

| Product | What | Price (INR, pay-as-you-go) |
|---|---|---|
| **Saaras v3** STT | 23 languages (22 Indian + English); transcribe / translate / **verbatim & code-mixed output modes**; **Realtime API (Aug 2026)**: WebSocket, true partials, mid-stream reconfig, ms-level VAD | **₹30/hour** (per-second billing); +diarization ₹45/hr; STT+translate ₹30/hr |
| **Bulbul v3** TTS | 11 languages (10 Indian + English), pitch/pace control | **₹30 per 10K characters** (v2 legacy ₹15) |
| **Sarvam-105B** LLM | 128K context flagship chat model | ₹29.28 in / ₹10.98 cached / ₹73.20 out per 1M tokens |
| Third-party open models hosted by Sarvam | Gemma-4 31B (₹36.6/₹91.5), GLM 5.3 Flash (₹13.5/₹45), DeepSeek V4 Flash (₹19.8/₹59.4) per 1M in/out | **This is an India-hosted open-weight inference option** |
| Mayura / Sarvam Translate | 11 languages | ₹20 per 10K chars; transliteration ₹20/10K; LID ₹3.5/10K |
| **Sarvam Vision / Doc Agents** | OCR + structured extraction, 23 languages | ₹0.50/page |
| **Voice Agents (Samvaad)** | Build/deploy/monitor voice + chat agents over phone and chat, 10 Indian languages | Not published |

Rupee math: 1 hour of owner voice ≈ ₹30 STT; a 30-second voice note ≈ ₹0.25. A 200-character TTS reply ≈ ₹0.60. Cheap enough to make voice the default input.

### 2.2 Government / open stack

- **Bhashini (MeitY)**: ULCA model registry; three-step API (pipeline search → config → compute) for **ASR, NMT, TTS**; models from IITB, IITM, IIITH, CDAC/AI4Bharat. Docs state usage is "for the PoC only" and commercial integrators must contact Bhashini for the paid version — so it is a prototyping rail, not a production SLA.
- **AI4Bharat (IIT Madras)** — all open:
  - **IndicConformer-600M-multilingual**: 22 scheduled languages, hybrid CTC+RNNT, **MIT licence**, Hindi WER **13.2** on ARTPARK Vaani benchmark (model card).
  - **IndicVoices**: 7,348 h (74% extempore, 17% conversational) from 16,237 speakers, 145 districts, 22 languages; 1,639 h transcribed (median 73 h/language); **CC BY 4.0**; basis of "IndicASR".
  - **Vistaar** benchmark (Interspeech 2023): 59 Indic ASR benchmarks; **IndicWhisper** (Whisper fine-tuned on 10.7K h, 12 languages) had the lowest WER on 39/59, avg −4.1 WER vs Google/Azure/AWS/vanilla Whisper.
  - **Indic Parler-TTS**: 21 languages, 69 voices, 1,806 h training, **Apache 2.0**, 0.9B params.
  - **IndicTrans2**: MT across all 22 scheduled languages; IndicXlit transliteration; Airavata LLM.
- **Hinglish / code-mixing**: Sarvam explicitly trains on code-mixed usage and offers code-mixed output; OkCredit ships "Hinglish" as one of its 11 UI languages — real SMB speech is Hinglish/Gujlish by default, so **evaluate STT on code-mixed, domain (amount, party name, item) utterances, not on read Hindi**. No public Hinglish WER leaderboard was fetchable this session **[unverified]**.
- **Krutrim (Ola)**: "Data stays in India" (Bangalore + Hyderabad), INR billing, LLM inference/fine-tuning, SOC 2, MCP server support; Indic speech pricing not published on the landing page.
- **Gnani.ai, Skit/Vernacular**: BFSI voice-agent vendors (Gnani is an IndiaAI Mission foundation-model awardee **[unverified]**); pages not fetchable.
- **Global**: **Gemini Live API** — 70 languages incl. Hindi and Bengali (Tamil/Telugu/Gujarati/Marathi/Kannada not explicitly listed), 16 kHz PCM in / 24 kHz out, still Preview. **ElevenLabs** TTS: Hindi + Tamil in Multilingual v2 (29 langs) and Flash v2.5 (32); v3 "70+ languages". **OpenAI Realtime** Hindi works but pricing page was 403 this session **[unverified: roughly $0.06–0.10/min audio-in at gpt-realtime rates]**.
- **Telephony**: Exotel contact-centre seats **$70 (XPrime) / $100 (XPro) per user/month**, with **"Voice Streaming (Agent Stream)"** sold separately — that streaming SIP/WebSocket product is what a voice agent needs. Knowlarity/Ozonetel/Twilio-India per-minute rates were not fetchable **[unverified: Indian PSTN termination typically ₹0.30–0.60/min via these CPaaS providers]**.

**Voice-first bookkeeping precedent**: Khatabook (5 crore+ downloads, 12 languages), OkCredit (1 crore+ merchants, 11 languages incl. Hinglish, offline, "2 minute mein setup"), Vyapar (1 crore+ users, offline billing) — none is voice-first yet, which is the opening.

---

## 3. Bank and payments data rails

### 3.1 RBI Account Aggregator (AA) framework

Ecosystem (Sahamati dashboard, June 2026): **17 AAs, 176 FIPs, 1,076 FIUs**, 2,880 M+ accounts enabled, 1,970 M+ consents raised, 493 M+ fulfilled, **295 M+ data shares/month**, ₹3.82 lakh crore credit powered.

| Fact | Status | Source |
|---|---|---|
| Savings accounts, GSTR returns, equities, MFs, NPS | Fully live | CASParser 2026 |
| **Current accounts** | "~65 of 72 banks support them" (DBS, HSBC, StanChart don't) | CASParser |
| **Partnership / company current accounts, joint, NRE/NRO** | **Explicitly excluded** from AA today | CASParser |
| **GSTN as FIP** | Notified Nov 2022; live with 13 AAs; shares **GSTR-1 (Table 4) + GSTR-3B for last 18 months**, filing details, profile (legal name, PAN, address) | GSTN AA PDF; einvoice6 note |
| Who can be an FIU | Must be regulated by RBI/SEBI/IRDAI/PFRDA; onboarding 5–10 months; first-year cost ₹5–25 lakh (TSP fees + per-consent) | CASParser |
| TSP integration | Setu "Bridge": sandbox with mock FIP for all 23 FI types → KYC → Sahamati FIU onboarding → prod creds (`x-client_id`/secret/product-instance-id) | docs.setu.co |
| Per-consent price | Not published by any TSP fetched (Setu/Finvu/Onemoney/Anumati/CAMS/Perfios) | — |

**Implication**: an un-regulated SaaS cannot be an FIU. Options: (a) partner with a regulated FIU (NBFC/bank) or a TSP that sponsors FIU access; (b) fall back to statement import; (c) sole-proprietor SMBs (the majority of Tally's base) *can* share their individual/proprietorship current account via AA, but Pvt Ltd/LLP/partnership current accounts cannot yet.

### 3.2 Bank statements and corporate bank APIs

- **Tally connected banking (fetched)**: live balances, statement fetch, reconciliation, payments from within TallyPrime for **Axis, SBI, ICICI, Kotak**; needs TallyPrime **6.0+** (banking/recon) and **7.0+** (connected payments); 2FA + TallyNet credentials; bundled with TSS. This is the bar to beat.
- Bank portals (ICICI API Banking/Connected Banking, Kotak API Banking, HDFC SmartHub Vyapar, Axis, Yes) all redirected to `*.bank.in` domains or timed out this session **[unverified]**: they offer ERP connectors (balance, statement, payment initiation, virtual accounts) under corporate-internet-banking onboarding, typically via a signed bank-ERP partnership rather than open developer portals.
- **Statement formats [unverified — prior knowledge]**: HDFC (XLS/CSV/PDF: Date, Narration, Chq./Ref.No., Value Dt, Withdrawal Amt., Deposit Amt., Closing Balance); ICICI (Tran Date, Cheque No, Particulars, Debit, Credit, Balance); SBI (Txn Date, Value Date, Description, Ref No./Cheque No., Debit, Credit, Balance); Axis/Kotak/Yes/IndusInd/PNB/BoB/Canara each vary in header row offsets and date formats. Perfios/Setu/ScoreMe sell PDF-statement analysers covering hundreds of formats; our own parser + LLM narration classifier (UPI/xxxx/VPA, NEFT/IMPS ref, cheque) is feasible — this repo already has `backend/internal/bankstatement/`.
- **UPI for business**: Razorpay — UPI has **zero MDR** by RBI policy but Razorpay charges a **2% platform/technology fee**; **payment links and invoices at no extra cost**; payment pages +0.2%; **UPI Autopay pricing "on request"**; RazorpayX payouts plans ~₹825–₹8,259/month. UPI intent/QR (static/dynamic), collect requests, and Autopay mandates are available through every PA (Cashfree, PayU, Setu, Decentro, Juspay). **[unverified]**: Autopay AFA-free limit ₹15,000 (higher for specified categories).
- **NPCI Bharat Connect for Business (B2B)**: Bharat Connect (ex-BBPS, run by NBBL) does 85 lakh payments/day, 22,000+ billers, 700+ digital channels. **Bharat Connect for Business** is live in **TallyPrime** (Tally webinar 7 Jan 2026): set up in TallyPrime → put the buyer's **Party B2B ID** on the ledger → send sales invoices over the network → buyer receives, pays, and both sides reconcile via reports. Zoho Books lists the same feature (page 403'd). **[unverified]**: launched at Global Fintech Fest Aug 2024. This is the single most important new rail: **interoperable invoice presentment + payment + auto-reconciliation across accounting platforms**, run by NPCI.
- **ONDC**: Beckn-protocol open network with B2B domain; stats page unfetchable **[unverified]**. Low priority.

---

## 4. Government rails

### 4.1 GST (GSTN via GSPs)

- **Architecture**: taxpayer enables "API access" on the GST portal → GSP/ASP calls taxpayer APIs after an **OTP to the registered mobile/email** produces a session token (MasterGST sandbox uses fixed OTP 575757; sandbox accounts are created on the GSTN developer portal by choosing a GSP). Return APIs: GSTR-1 save/submit/file, GSTR-3B, GSTR-2A/2B fetch, GSTR-9, ledgers (cash/credit/liability), plus e-invoice (IRP) and e-way bill (NIC) APIs.
- **IMS (Invoice Management System)**: launched **1 Oct 2024**, actions from 14 Oct 2024. Recipient marks each inward invoice **Accept / Reject / Pending**; no action = **deemed accepted** into GSTR-2B. APIs (Sandbox.co.in docs): Get Invoice Count, Get Invoices (by section/status), Save Invoice Status (A/R/P), Reset, Check Status (async ref ID), supplier-side Get Sales Invoices (recipient responses) and Get Added-Back Liabilities. GSTN has since released credit-note-action APIs in sandbox.
- **GSPs**: seven dominate the API resale market — **WhiteBooks, ClearTax, Masters India, Sandbox (Quicko), IRIS, GSTHero, Cygnet** (Cygnet also runs IRP "einvoice6"); others: Vayana, Zoho, Alankit, TaxPro/Chartered Info, Tally (own GSP) **[GSP list page unfetchable]**. Pricing is quote-based (MasterGST: contact sales; Sandbox: cost calculator, pay-per-call). **[unverified — prior knowledge]**: typical resale ₹0.50–₹3 per API call with monthly minimums.
- **GSTIN verification**: GST public search API (via GSP) returns legal/trade name, status, state, registration date, filing table.
- **Geo-fencing (memory note)**: NIC 164.100.x hosts (e-way bill, IRP sandbox) and RBI docs are fenced to Indian IPs — our backend must egress from India regardless of where inference runs.

### 4.2 Filing legality — who may press "file"?

**[Largely unverified this session — pages 404'd; stated from prior knowledge of CGST Rules]**
- **Rule 26 CGST Rules**: every return/application must be verified by the **authorised signatory** using **DSC (mandatory for companies and LLPs)** or **EVC (OTP to the signatory's registered mobile/e-mail; Aadhaar-based)**. A software vendor cannot hold the signatory's DSC/Aadhaar-OTP; the OTP arrives on the human's phone.
- **Section 48 + Rule 83 (GST Practitioners)**: a GSTP may furnish returns on a taxpayer's behalf, but **Rule 83(8) requires the registered person to confirm** the statement, and responsibility for correctness stays with the taxpayer.
- **eSign (CCA, fetched)**: Aadhaar e-KYC (OTP/biometric) → one-time key pair on an HSM, destroyed after single use; only the document hash is signed; legally valid under the IT Act. Acceptable for **our own** documents (engagement letters, invoices, board resolutions), not a substitute for GST portal EVC/DSC.
- **Consequence for the agent**: the agent may **prepare, validate, reconcile (2B vs books), populate, and "save" GSTR-1/3B via API**, and may **stage IMS accept/reject decisions**; the **file** step requires the human's OTP/DSC in the loop. Design the last mile as "review → tap → OTP".

### 4.3 Income tax, MCA, KYC rails

- **ERI (e-Return Intermediary)**: Type-1/2/3 registration with the Income Tax Dept enables prefill JSON, 26AS/AIS fetch and ITR submission with taxpayer Aadhaar-OTP/EVC **[unverified — portal pages 404]**. **TRACES** (TDS) has no public API; 26AS/AIS are pulled via ERI or portal scraping.
- **MCA21 V3**: company master data (CIN, status, directors, capital) is exposed by KYC aggregators (Sandbox, etc.) **[unverified]**.
- **Udyam**: portal states no third-party system is authorised to register; verification is via the certificate's dynamic QR / "Verify Udyam" page — no public API (fetched).
- **PAN verification**: Protean (ex-NSDL) online PAN verification (screen/file/API) with registration fee and per-PAN charges **[details unverified — page is a JS shell]**.
- **DigiLocker / Entity Locker**: Entity Locker (organisational DigiLocker linking GST, MCA, Udyam, PAN) — DNS did not resolve this session **[unverified]**.

---

## 5. Marketplace, POS, and commerce data

| Source | Access | What the agent learns |
|---|---|---|
| **Flipkart Seller API** (fetched) | OAuth2: client-credentials (seller self, token ~60 days) or authorization-code for partners (refresh token ~180 days); partner verification ≤72 h | Orders, shipments, returns, **settlement reports**, listings → marketplace sales, fees, TCS/TDS, returns |
| **Amazon SP-API** | Redirect chain broke this session **[unverified]**: Finances API (listFinancialEventGroups/Events), Reports (settlement) | Settlements, fees, refunds by order |
| **Meesho, Dukaan, Zomato, Swiggy** | No public seller APIs (portal exports/emails) **[unverified]** | Payout statements → parse PDF/CSV/email |
| **Shopify / WooCommerce / Zoho Inventory** | Standard REST/GraphQL | Orders, payouts |
| **Petpooja POS** | Integrations with Swiggy/Zomato/Tally/Zoho advertised; API access is partner-only **[page fetch hit limit]** | Daily sales, aggregator commissions |
| **IndiaMART Lead Manager Pull API v2** (fetched) | `mapi.indiamart.com/wservce/crm/crmListing/v2/`, key `glusr_crm_key` (expires after 7 idle days); ≤5 req/min, ≥5 min apart, 7-day windows, 365-day history | Leads: UNIQUE_QUERY_ID, SENDER_NAME/MOBILE/EMAIL, QUERY_TYPE (W/B/P/BIZ/WA), QUERY_MESSAGE → pipeline & customer master |
| **Shiprocket / Delhivery** | Shiprocket API: orders, AWB, tracking, invoice/e-way bill fields, wallet & **COD remittance** | Freight cost, COD receivables, e-way bill data |
| **Google Business Profile** | GBP API | Reviews/footfall — low finance value |

Auto-reconciliation targets: marketplace settlement ↔ bank credit; COD remittance ↔ bank; aggregator commission ↔ P&L; TCS deducted ↔ GSTR-2B/26AS.

---

## 6. Regulatory constraints for an AI agent on SMB financial data

### 6.1 DPDP Act 2023 + DPDP Rules 2025 (verified)

- Rules notified **13/14 Nov 2025**. Phasing: **Data Protection Board live now**; **Rule 4 Consent Manager registration mandatory from 13 Nov 2026**; **Rules 3, 5–16 (notice, security safeguards, breach, retention, children, SDF, cross-border) and the penalty regime enforceable from 13 May 2027**.
- **Rule 6 safeguards**: encryption in transit and at rest, need-to-know access control, MFA, access logging/monitoring, vulnerability assessment, documented incident response. **Logs ≥1 year** (general); e-commerce/social/gaming retention 3 years with 48-h erasure notice.
- **Rule 7 breach**: notify **every** breach (no risk threshold) — Data Principals "without delay", **DPB within 72 hours**.
- **Rule 8 retention/erasure**: purpose-bound, automated erasure.
- **Cross-border**: **negative list** (transfers permitted except to notified countries; no list notified yet). SDFs may later face localisation of specified data; **no SDF designated yet**; sensitive-data categories not yet specified.
- **Penalties (Act Schedule)**: **₹250 cr** (security safeguards), **₹200 cr** (breach notification); **[unverified — prior knowledge]** ₹200 cr children, ₹150 cr SDF duties, ₹50 cr other.
- **Consent Managers** must be Indian companies with net-worth thresholds and interoperable specs (pending DPB). Relevant to us: AA-style consent UX for data we pull about *customers of our customers* (their debtors' phone numbers on WhatsApp).

### 6.2 CERT-In Directions (28 Apr 2022, verified from the PDF)

Apply to "service providers, intermediaries, data centres, body corporate and Government": **report listed incidents within 6 hours**; **retain ICT logs for a rolling 180 days within India**; sync clocks to **NIC/NPL NTP**; cloud/VPS/VPN providers keep subscriber KYC **5 years**; effective 60 days after issue (27 Jun 2022). Dual clock with DPDP: 6 h CERT-In, 72 h DPB.

### 6.3 RBI (only if we become a regulated entity or their vendor)

- **Payment System Data storage (6 Apr 2018)**: entire end-to-end payment data of payment system operators and banks must be stored **only in India** (foreign-leg exception); compliance by 15 Oct 2018; CERT-In-empanelled system audit. **[unverified]** FAQ allows processing abroad if data returns within 24 h and is deleted abroad.
- **Master Direction on IT Outsourcing (Apr 2023)**: REs must do due diligence, contract for **India data location**, audit rights, exit plans; **Appendix I** covers cloud/SaaS multi-tenancy, IAM/MFA, shared responsibility. If a bank/NBFC uses our product or we act as their TSP/FIU sponsor's vendor, we inherit these.

### 6.4 Company-law and profession

- **MCA audit trail (edit log)**: Companies (Accounts) Rules — since **1 Apr 2023** every company's accounting software must log each change with timestamp, **cannot be disabled**, retained **8 years**; auditors report under Rule 11(g); penalties ₹25k–₹5 lakh (adjudication orders 2024–25). **Agent-made edits must be attributed to a principal (user id + "agent on behalf of") in the immutable log.**
- **ICAI**: Committee on AI exists (site unfetchable); no binding AI standard yet **[unverified]** — CAs remain responsible for opinions; agent output must be reviewable.
- **MeitY India AI Governance Guidelines (Nov 2025)** **[unverified — pages 500/403]**: principles-based, no new AI statute, rely on DPDP/IT Act, AI Safety Institute, incident database, voluntary transparency.
- **IT Rules amendment on "synthetically generated information"** **[unverified]**: draft Oct 2025, notified early 2026 — mandatory labelling/metadata for AI-generated content on intermediaries; low direct impact on a B2B finance agent but label agent-authored customer messages.

### 6.5 Hosting stance: US inference vs India

- **Fireworks regions (fetched)**: US (16 sites), EU (Frankfurt, Iceland), APAC (**Malaysia, New South Wales, Tokyo**), Canada. **No India region.** Multi-region groups GLOBAL/US/EUROPE/APAC.
- **Legal read**: DPDP's negative-list model means US inference is **lawful today** (until a blacklist is notified), with contract, transfer-register and Rule-6 safeguards. Restrictions bite only if (a) we handle RBI-regulated payment data as a PSO/bank vendor (India-only storage), or (b) we are designated SDF with localisation. CERT-In's 180-day logs must be in India regardless.
- **Commercial/trust read**: Indian banks, CAs and large SMBs will ask "where is my ledger processed?". India-hosted open-weight options exist now: **Sarvam** (hosts Gemma-4 31B, GLM 5.3 Flash, DeepSeek V4 Flash in INR), **Krutrim** (Bangalore/Hyderabad, SOC 2, INR), **E2E Networks** (H100 $3.77/h ≈ ₹315, H200 $4.54, B200 $6.99, L40S $1.20, L4 $0.57, per-minute billing), **Yotta Shakti** (NM1 Navi Mumbai; pricing unfetchable), **AWS Bedrock ap-south-1** (Mumbai; open models via cross-region inference — model list page moved, unverified), Azure Central India, GCP Mumbai. **IndiaAI compute portal** offers subsidised GPUs (~38,000 GPUs; **[unverified]** ~₹65–100/hr H100 for eligible startups).

---

## 7. Channel UX evidence (what Indian SMB owners actually use)

Verified product facts stand in for surveys (survey PDFs were not fetchable this session):
- **OkCredit**: 1 crore+ merchants, 2,800+ cities, **11 languages incl. Hinglish**, offline-first, WhatsApp-like UI, automatic SMS reminders, UPI collection, "2 minute mein setup". Design lesson: Hinglish is a *language option*, not an error case.
- **Khatabook**: 5 crore+ downloads, "5 Crore registered merchants across 12 languages"; Android-only at launch (smartphone-only owners).
- **Vyapar**: 1 crore+ users, offline billing, free Android tier, WhatsApp invoices/estimates/bulk reminders.
- **Tally**: even the desktop incumbent now routes invoices, UPI links and payment collection through WhatsApp and Bharat Connect.
- **Meta**: introduced INR billing specifically for India (Jan 2026) — India is WhatsApp's largest business-messaging market **[scale figures unverified: ~500 M+ users, hundreds of millions of daily business conversations]**.
- **Voice**: Sarvam prices STT at ₹30/hr and ships realtime + code-mixed modes; Gnani/Skit built BFSI voice bots; IndicVoices' corpus is 74% extempore speech — the ecosystem is optimising for spoken Hinglish, which is the signal that owners talk rather than type. **[No fetched survey on % of MSMEs using voice — unverified.]**

---

## 8. Recommendations for our build

### 8.1 Channel order
1. **WhatsApp (Cloud API direct, own WABA) — first.** Owner↔agent conversation is free (service window). Use voice notes (OGG/Opus ≤16 MB) + invoice photos (≤5 MB) + PDFs (≤100 MB) as primary inputs; persist media on webhook (5-min URLs). Use **Flows** for structured confirmations (match this bank line? approve this GSTR-3B summary?). Send debtor reminders as **utility templates (₹0.115)**, with UPI payment link/QR; ride the 24-h window whenever the debtor replies. Start via a BSP (Interakt/AiSensy) for speed if we want WhatsApp Payments/Forms out of the box; move to direct Cloud API + INR billing at scale.
2. **Voice (in-app + WhatsApp voice notes, then PSTN).** Sarvam Saaras v3 streaming for Hinglish/Indic STT (₹30/hr), fall back to IndicConformer (MIT) self-hosted for cost/residency; Bulbul v3 or Indic Parler-TTS (Apache 2.0) for replies. PSTN voice agent via Exotel Agent Stream only after WhatsApp voice proves the intent grammar ("₹X <party> ko diya/liya/bill").
3. **Web/desktop** remains the CA/accountant surface; SMS only for OTP/critical alerts under DLT templates; skip RCS/Telegram for v1.

### 8.2 Ingestion map — every source → what the agent learns

| Source | Rail | Agent learns |
|---|---|---|
| Bank current a/c | AA (via regulated FIU partner) for proprietors; statement PDF/CSV import for cos/LLPs; connected-banking APIs (Axis/SBI/ICICI/Kotak parity with Tally) later | Cash position, receipts↔invoices, payments↔bills, bank charges, loan EMIs, UPI VPA counterparties |
| UPI/PG | Razorpay/Cashfree/PayU webhooks; payment-link status | Collections, settlement lag, fees, refunds |
| **Bharat Connect for Business** | NBBL network via TSP | Interoperable invoice presentment/payment; automatic AR/AP matching with the counterparty's books |
| GST portal | GSP APIs (OTP session) | GSTR-2B/IMS inward invoices (supplier claims), 2A vs books gaps, ITC eligibility, ledger balances, filing status |
| GSTN via AA | AA (GSTR-1/3B, 18 months) | Turnover trend for credit/insights without portal OTP |
| e-invoice / e-way bill | IRP/NIC via GSP (India egress) | IRN/ack, e-way bill validity, movement of goods |
| Income tax | ERI (26AS/AIS) | TDS deducted by customers ↔ receivables; TCS from marketplaces |
| Marketplaces | Flipkart API, Amazon SP-API, payout PDFs | Sales, commissions, returns, settlements |
| POS / aggregators | Petpooja partner API, Swiggy/Zomato payout files | Daily sales, commissions |
| Leads | IndiaMART pull API | Customer master, pipeline |
| Logistics | Shiprocket/Delhivery | Freight expense, COD receivable, e-way bill fields |
| WhatsApp/email inbox | Cloud API webhooks, IMAP | Supplier bills (photo/PDF → Sarvam Vision ₹0.5/page or our OCR), customer promises-to-pay |
| KYC | GST public search, PAN verify, MCA master, Udyam QR | Party onboarding, GSTIN validity, B2B/B2C classification |

### 8.3 DPDP-compliant hosting stance
- **Data plane in India** (ap-south-1 / Mumbai): ledgers, documents, AA payloads, **all logs (CERT-In 180 d; DPDP ≥1 yr)**, audit trail (MCA 8 yrs). NTP to NIC/NPL.
- **Inference**: Phase 1 may use Fireworks (US/APAC-Tokyo/Malaysia) under a processor contract, transfer register, zero-retention, PII minimisation/tokenisation of party names and account numbers before prompts. Phase 2 (before **13 May 2027** enforcement, and before any bank/NBFC channel deal): India-resident open-weight inference — Sarvam-hosted open models, Krutrim, or self-host on E2E/Yotta (Llama/Qwen/DeepSeek class at ~₹315/H100-hr); keep Fireworks as burst/fallback for non-PII workloads.
- Build the **72-h DPB + 6-h CERT-In** incident playbook once; publish a DPDP notice in Indic languages; verifiable consent for any debtor/customer data we message; consent-manager-ready APIs by Nov 2026.
- Never become a payment system operator; use PAs, so RBI data-localisation applies to them, not to our inference layer.

### 8.4 Filing-legality boundaries (agent may / must hand off)
| Agent may do autonomously | Agent prepares, human confirms | Human/CA only |
|---|---|---|
| Classify transactions, match bank↔books, draft vouchers, compute GST/TDS, reconcile 2B/IMS vs purchases, generate GSTR-1/3B JSON, **save** to portal via GSP, stage IMS accept/reject, send utility reminders, generate e-invoice IRN requests (with signatory-approved templates) | **File** GSTR-1/3B/9 (EVC OTP/DSC by authorised signatory), submit IMS actions that change ITC, approve bank payouts, sign eSign documents, submit ITR/TDS returns (ERI + taxpayer OTP) | Audit opinions, tax positions in dispute, DSC custody, GSTP confirmation under Rule 83(8), DPDP breach notifications sign-off |

Every agent write must carry a principal id and reason string into the MCA-grade audit trail, and the UI must show "prepared by agent, filed by <name> via EVC at <time>".

---

## Sources (fetched or seen this session)

1. https://developers.facebook.com/docs/whatsapp/pricing
2. https://developers.facebook.com/docs/whatsapp/cloud-api/reference/media
3. https://developers.facebook.com/docs/whatsapp/flows/guides
4. https://whatsappbusiness.com/products/whatsapp-flows/
5. https://blueticks.co/blog/whatsapp-business-api-pricing-2026
6. https://blueticks.co/blog/whatsapp-business-pricing-marketing-messages-2026
7. https://www.engagelab.com/blog/whatsapp-business-api-pricing
8. https://www.authgear.com/post/whatsapp-api-pricing/
9. https://routemobile.com/blog/whatsapp-business-pricing/
10. https://aisensy.com/pricing
11. https://www.interakt.shop/pricing/
12. https://www.wati.io/pricing
13. https://tallysolutions.com/business-guides/whatsapp-invoicing-payments-india-2026/
14. https://getswipe.in/blog/collections/can-you-send-professional-invoices-through-whatsapp
15. https://anantya.ai/blog/whatsapp-business-api-india-2025-guide/
16. https://ominiflow.com/blog/invoice-payment-reminder-whatsapp
17. https://developers.google.com/business-communications/rcs-business-messaging/guides/get-started/how-it-works
18. https://docs.sarvam.ai/api-reference-docs/pricing
19. https://docs.sarvam.ai/
20. https://invideo.io/blog/sarvam-bulbul-indian-tts/
21. https://marketplace.microsoft.com/en-us/product/saas/sarvam.sarvam_api_prod?tab=overview
22. https://bhashini.gitbook.io/bhashini-apis
23. https://ai4bharat.iitm.ac.in/
24. https://huggingface.co/ai4bharat/indic-conformer-600m-multilingual
25. https://huggingface.co/ai4bharat/indic-parler-tts
26. https://arxiv.org/abs/2403.01926 (IndicVoices)
27. https://arxiv.org/abs/2305.15386 (Vistaar / IndicWhisper)
28. https://ai.google.dev/gemini-api/docs/live
29. https://elevenlabs.io/docs/overview/capabilities/text-to-speech
30. https://www.olakrutrim.com/
31. https://exotel.com/pricing/enterprise-contact-center/
32. https://sahamati.org.in/
33. https://casparser.in/blog/state-of-account-aggregator-2026/
34. https://hyperverge.co/blog/account-aggregator-framework-rbi/
35. https://www.gstn.org.in/assets/mainDashboard/Pdf/account-aggregator.pdf
36. https://einvoice6.gst.gov.in/content/account-aggregator-frameworks-inclusion-of-gstn-as-a-financial-information-provider/
37. https://docs.setu.co/data/account-aggregator/overview
38. https://docs.setu.co/data/account-aggregator/quickstart
39. https://financialservices.gov.in/account-aggregator-framework
40. https://tallysolutions.com/tally/connected-banking/
41. https://tallysolutions.com/business-guides/bharat-connect-for-business/
42. https://www.bharat-connect.com/
43. https://razorpay.com/pricing/
44. https://developer.sandbox.co.in/api-reference/gst/compliance/guides/taxpayer/invoice-management-system/overview
45. https://sandbox.co.in/pricing
46. https://mastergst.com/gst/gst-developer-api-portal.html
47. https://whitebooks.in/best-gst-api-india/
48. https://go4gst.com/all-about-gst-invoice-management-system-ims-gst-ims-api/
49. https://developer.gst.gov.in/apiportal/
50. https://cca.gov.in/eSign.html
51. https://udyamregistration.gov.in/Udyam_Verify.aspx
52. https://seller.flipkart.com/api-docs/FMSAPI.html
53. https://help.indiamart.com/knowledge-base/lms-crm-integration-v2/
54. https://apidocs.shiprocket.in/
55. https://en.wikipedia.org/wiki/Digital_Personal_Data_Protection_Rules,_2025
56. https://www.matters.ai/compliance/dpdp/dpdp-rules-2025
57. https://consentos.in/learn/dpdp-compliance-timeline/
58. https://www.cy5.io/blog/dpdp-rules-2025-complete-compliance-guide-cloud-security/
59. https://www.scrut.io/post/dpdp-rules
60. https://www.cert-in.org.in/Directions70B.jsp
61. https://www.cert-in.org.in/PDF/CERT-In_Directions_70B_28.04.2022.pdf
62. https://www.rbi.org.in/Scripts/NotificationUser.aspx?Id=11244
63. https://www.rbi.org.in/Scripts/NotificationUser.aspx?Id=12486
64. https://cleartax.in/s/audit-trail-applicability
65. https://docs.fireworks.ai/deployments/regions
66. https://www.e2enetworks.com/pricing
67. https://docs.aws.amazon.com/bedrock/latest/userguide/models-regions.html
68. https://vyapar.com/
69. https://okcredit.in/
70. https://khatabook.com/about-us
