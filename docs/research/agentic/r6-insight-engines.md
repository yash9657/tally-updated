# R6 — Insight / Recommendation / Analytics Engines for Indian SMB Business Data

Research date: 2026-09-08. Scope: what an "insight engine" over a Tally-shaped dataset (ledger, invoices with HSN/GST line items, parties, stock/godowns/batches, bank statements, payroll, GST returns, orders, cost centres, multi-year history) should compute, how, with which libraries, and how competitors surface it. Sources were fetched directly; where a claim is general practice rather than a fetched fact, it is labelled "standard practice".

---

## 0. Executive summary (what the evidence says)

1. **Deterministic numbers, LLM words.** Every serious commercial "insights" product (Tableau Pulse, ThoughtSpot SpotIQ, Zoho Zia, Power BI Copilot) computes facts with fixed statistical models and lets the LLM only narrate. Tableau's docs say the Insights platform "uses standardized, deterministic statistical models to detect facts about metrics that are guaranteed to be accurate" and ranks them by "statistical impact" plus user feedback. This is the architecture to copy.
2. **A semantic/metric layer is the single biggest accuracy lever for NL analytics.** Two independent 2026 benchmarks: Cube's paired McNemar test (n=99) shows +17 to +23 pp accuracy across Claude Opus 4.7 / Sonnet 4.6 / GPT-5.4 (from ~46-51% to ~68%) from a 4 KB semantic markdown; dbt Labs' MetricFlow benchmark shows Sonnet 4.6 90.0% -> 98.2% and GPT-5.3-Codex 84.1% -> 100% on in-scope questions, and crucially the semantic-layer path "fails with error messages (never silently returns wrong data)" whereas raw text-to-SQL "can return plausible, but incorrect answers".
3. **Late-payment prediction is a solved, cheap tabular-ML problem.** A 2025 KTH/Hilti thesis on ~40k B2B orders: LightGBM AUC > 0.85, PR-AUC > 0.96, F ~0.94, with SHAP drivers "past average delay, open credit and failed credit checks"; days-late regression works in one market (MAE ~15 days) and is unstable in another, so predict *probability of late* and a *quantile* of days-to-pay, not a point estimate. Vendors (Growfin "94% forecasting accuracy", Chaser "payer ratings", YayPay "predict when each individual invoice will be paid") productise exactly this.
4. **Intermittent SKU demand: use Croston/TSB/ADIDA/IMAPA as the floor, a foundation model (Chronos-2 / TimesFM-2.5) as the ceiling, evaluate on your own tenants.** Nixtla's M5 study: TimeGPT MAE 0.493 vs best classical (CrostonClassic 0.565), ~12.8% better; M5 winners were all LightGBM global models but "lacked robustness" at bottom levels where "simpler methods effectively modelled intermittent or lumpy time series". Prophet is now the slow, weak option (StatsForecast claims 500x faster; ThoughtSpot still uses Prophet only for >30-point series).
5. **Alert fatigue is the failure mode.** Fraud/AML transaction monitoring runs at ~93% false positives in 2025 (from 97% in 2015); SOCs see ~9,854 false positives/week. An SMB owner will mute a digest that is wrong twice. Ranking, suppression, and a feedback loop are not optional; Google Analytics, Tableau Pulse and Duolingo all learn from per-user feedback.
6. **Personalised timing/content of nudges is a bandit problem with known solutions.** Duolingo's Recovering Difference Softmax (KDD'20) handles novelty decay and eligibility-gated templates; +0.5% DAU and +2% new-user retention over a strong baseline on millions of daily reminders.

---

## 1. Customer & sales intelligence on invoice data

### 1.1 Segmentation and lifetime value

| Technique | What it needs from Tally-shaped data | Method / library | Notes |
|---|---|---|---|
| RFM (Recency, Frequency, Monetary) | Sales vouchers per party: last invoice date, count, net value over 12 m | Quintile scoring in SQL; k-means on log-scaled RFM for named segments | Standard practice. Compute per tenant nightly; store as party features. |
| CLV (non-contractual B2B) | Invoice dates + values per party | BG/NBD (purchase frequency, P(alive)) + Gamma-Gamma (monetary) via **PyMC-Marketing** | `lifetimes` is "no longer maintained" and "transitioned into PyMC-Marketing" which ships BG/NBD, Pareto/NBD, Shifted-BG, BG/BB and Gamma-Gamma with Bayesian uncertainty. |
| Churn / inactivity for B2B | Per-party inter-order intervals | Compare time-since-last-order to that party's own cadence distribution (e.g. > p90 of historical gaps, or BG/NBD P(alive) < 0.3) | Standard practice; P(alive) from BG/NBD is the principled version of "haven't ordered in a while". |
| Survival view of churn | Same | Kaplan-Meier / Cox PH / Weibull AFT via **lifelines** (right/left/interval censoring, time-varying covariates) | Gives "median days until next order" with CI. |

### 1.2 Cross-sell / "which customers to push product X to"

Invoice line items are implicit feedback (party x item x qty). Three complementary methods:

- **Market-basket / association rules** — `mlxtend.frequent_patterns.fpgrowth` (no candidate generation; ~2.6x faster than apriori in mlxtend's own toy benchmark, handles nulls) then `association_rules` for support/confidence/lift/leverage/conviction/Zhang's metric. Best for "customers who buy A also buy B" explanations.
- **Collaborative filtering on implicit feedback** — `implicit` (ALS with conjugate-gradient solver, BPR, Logistic MF, item-item cosine/TF-IDF/BM25; Cython+OpenMP multithreaded; CUDA kernels; ANN via Annoy/NMSLIB/Faiss). `LightFM` hybrid MF adds side features (HSN, category, party state/type) which matters for cold-start SKUs.
- **A published B2B distributor case (Quebec distributor, Intelligent Systems with Applications, Jan 2025)** combined association-rule mining, implicit ALS (i-ALS) and SVD++ initialisation: taxonomy from product text, user-item matrix *extended with association-rule complements*, then i-ALS. This is the closest published analogue to "Tally sales data -> recommendations".
- **item2vec / prod2vec** (word2vec over invoice baskets) is standard practice for item similarity; useful for "substitute suggestion when out of stock".

Turn recommendations into propensity: score = P(party buys item in next 30 d) from ALS/LightFM rank blended with (a) rule lift, (b) party RFM, (c) item margin, (d) seasonality lift for the upcoming month. Surface as "push item X to these 12 customers; expected margin Rs Y".

### 1.3 Price sensitivity and margin per customer

Tally data has per-line rate, discount, and (via purchase cost / batch) landed cost. Standard practice: per (party, item) compute realised price vs list, discount depth, and elasticity via log-log regression of qty on price within party-item panels (only where >= 8 observations); flag "customers paying below floor margin" and "items where a 2% price rise would not have lost volume". Report as ranges, not point elasticities.

### 1.4 Credit risk, late payment, days-to-pay

Evidence base:

| Source | Data | Model | Result |
|---|---|---|---|
| KTH/Hilti "Pay Me Maybe" (Jun 2025) | ~40,000 credit-blocked B2B orders, 2 EU markets | LR vs 3 GBDTs; LightGBM best; SHAP for explanations | AUC > 0.85, PR-AUC > 0.96, F ~0.94; drivers = past avg delay, open exposure, failed credit checks; delay-length regression MAE ~15 days in one market, unstable in the other; market-specific models beat a global one |
| Appel et al., arXiv 1912.10828 (IBM, bank AR) | Real AR data | Prototype ML | "up to 77% accuracy", used to prioritise collectors |
| Framework paper, Machine Learning with Applications 2024 (S2666827024000549) | Invoice-level | Features from past customer behaviour; survival + boosting ("Survival Boost") | Random Survival Forests with debtor history "perform best in ranking payment times of late invoices" (from search summary) |
| Intuit QuickBooks Payments Agent (2025-26) | QBO customers | Predicts late payments, drafts reminders, suggests late fees | Marketing claim: paid "an average of 5 days faster" |
| Growfin / Chaser / YayPay(Quadient) / Tesorio | AR SaaS | Behavioural ML on payment history, AP-run dates, fiscal calendars | Growfin "94% forecasting accuracy" (vendor claim), Chaser "payer ratings" + "recommended chase times", Tesorio flags "typical AP run dates" and anomalous slowdowns |

Feature set for our build (all derivable from Tally-shaped data): party's mean/median/p90 days-to-pay (last 6/12 m), trend of days-to-pay, % invoices paid late, current open exposure vs credit limit, invoice amount relative to party's median, invoice day-of-month (AP-run effects), due-day weekday, festival proximity, number of partial payments, disputes/credit notes ratio, party state/inter-state, GST filing status of the party (supplier side), age of relationship, our own reminder history.

Models: LightGBM/CatBoost classifier for P(late > k days) + `scikit-survival` (Random Survival Forest, Gradient Boosting Survival, penalised Cox; evaluate with concordance index, integrated Brier score, time-dependent AUC) for full days-to-pay distribution. Report the p50 and p80 expected payment date, as YayPay does per invoice.

Context numbers that justify the feature: QuickBooks 2026 US report — 59% of small businesses have invoices 30+ days overdue (47% in 2025), average unpaid balance $17.7K, 39% say late payments made payroll/bills hard; Xero US: invoices paid 9.0 days late on average (June qtr 2026). Indian MSMEs face the same structure (MSME Pulse Jul 2026: Rs 65.8 lakh crore outstanding; recent unsecured business-loan originations show 2.9x higher 90+ DPD).

### 1.5 Optimal reminder timing

Standard practice: reminder policy as a small contextual bandit or uplift model — arms = {T-3 soft, T+1, T+7 firm, phone-call suggestion}, context = party risk score and predicted p50 pay date, reward = paid within 7 days of reminder. Chaser markets "recommended chase times"; QuickBooks "drafts proactive invoice reminders" based on inconsistent payment patterns. Start with rules (send at p50 - 2 days if P(late) > 0.4), log outcomes, graduate to a bandit after ~1,000 reminder outcomes per tenant cohort.

---

## 2. Inventory intelligence

### 2.1 Demand forecasting for intermittent SMB demand

Evidence:

- **StatsForecast** (Nixtla) ships ADIDA, CrostonClassic, CrostonOptimized, CrostonSBA, IMAPA, TSB plus AutoARIMA/AutoETS/AutoTheta/MSTL/TBATS; claims 20x faster than pmdarima, 500x faster than Prophet, 4x faster than statsmodels, "1,000,000 series in 30 min" with Ray. This is the right classical engine for thousands of SKU x godown series per tenant.
- **Nixtla M5 intermittent study**: MAE — TimeGPT 0.493, TimeGPT+event exogenous 0.485, CrostonClassic 0.565, TSB 0.567, IMAPA 0.572, CrostonOptimized 0.581. Foundation model ~12.8-14% better than the best classical method on food-category M5 data.
- **M5 competition**: ~60% of bottom-level sales are zeros; top-50 all tree ensembles (LightGBM) with cross-learning; 22% better WRMSSE than seasonal naive; but LightGBM global models "lacked robustness" while "simpler methods effectively modelled intermittent or lumpy series".
- **Foundation models (2025-26)**: Chronos-2 (Oct 2025) adds group attention for in-context multivariate/covariate forecasting and claims SOTA on fev-bench, GIFT-Eval and Chronos Benchmark II; TimesFM-2.5 (200M params, 1024 context, up to 256-step horizon, continuous quantile head 10-90th pct, Apache-2.0, ~1.8M monthly downloads); Moirai-2.0 (decoder-only, 11.4M small, quantile loss, multi-token prediction) but **CC-BY-NC-4.0 — research only**, so not usable commercially. GIFT-Eval: 97 tasks over 55 datasets; Chronos-2 reported above TiRex and TimesFM-2.5 on WQL and MASE win rates.
- **Combining probabilistic intermittent forecasts** (arXiv 2304.03092): combinations beat individual methods "but there is a trade-off between forecasting and inventory performance" — optimise for stock-out cost, not MAE.
- **Prophet**: ThoughtSpot still uses "Facebook's Prophet library" for >30-point series; Amplitude and GA don't disclose. The independent microprediction critique was not reachable (redirect chain 404), so treat "Prophet's decline" as a practitioner consensus supported by StatsForecast's speed numbers and its absence from GIFT-Eval leaders, not a fetched benchmark.
- **HierarchicalForecast** reconciles item -> category -> company and godown -> region forecasts (BottomUp, TopDown, MiddleOut, MinTrace, ERM; probabilistic Normality/Bootstrap/PERMBU/Conformal). Needed because the owner wants both "company sales next month" and "SKU X at godown Y".

Recommendation: classical ensemble (TSB + ADIDA + IMAPA + SeasonalNaive) as baseline per SKU; Chronos-2 or TimesFM-2.5 zero-shot with festival covariates for A/B-class items; pick per-series via rolling-origin backtest on the tenant's own history (Nixtla's `cross_validation`). Output quantiles (p50/p90), never a single number.

### 2.2 Inventory policy metrics (standard practice; all deterministic)

| Insight | Formula / method | Data |
|---|---|---|
| ABC-XYZ | ABC by cumulative value share (80/15/5); XYZ by coefficient of variation of monthly demand (< 0.5 / 0.5-1 / > 1) | Stock item ledger |
| Reorder point | ROP = d_bar x L + z x sigma_dL (use quantile forecast at target service level instead of normal z when intermittent) | Forecast + lead time |
| Safety stock | z x sqrt(L x sigma_d^2 + d_bar^2 x sigma_L^2) | Demand + lead-time variance |
| EOQ | sqrt(2DS/H) | Annual demand, order cost, holding cost |
| Dead / slow stock | No outward movement in N days (N by category) AND on-hand > 0; value at cost | Stock movements |
| Expiry risk | Batches with expiry < forecasted sell-through date | Batch master + forecast |
| Supplier lead time | Distribution (median/p90) of PO date -> GRN date per supplier; flag drift | Purchase orders, receipts |
| Price creep | Per (supplier, item) rolling rate; flag rate > p90 of last 12 m or > CPI-ish threshold | Purchase vouchers |
| Margin by SKU / customer | Sales line net - cost (FIFO/avg from Tally) | Sales + stock valuation |
| Stock-out cost | Lost qty (forecast - available) x margin per unit | Forecast + on-hand |

### 2.3 Indian seasonality

The `holidays` library (250 country codes) covers India with 36 subdivisions and includes Diwali, Holi, Dussehra, Navratri, Janmashtami, Ganesh Chaturthi, Onam, Pongal, Eid al-Fitr/Adha, Muharram, Guru Nanak Jayanti, Christmas; Hindu dates are computed via `HinduCalendarHolidays` (valid 2001-2035) with static per-year overrides (e.g., Maharashtra Holi 2026 = Mar 3). Use it to generate covariates: days-to-Diwali, Navratri window, wedding season (Nov-Feb, Apr-Jun — build from panchang "muhurat" tables, not in `holidays`), monsoon (Jun-Sep, regional), fiscal year-end March push, GST/quarter-end effects. Chronos-2 explicitly benefits from such covariates ("consistently outperforms baselines" on covariate tasks); Nixtla's M5 example gained ~1.5 pp from event-type exogenous.

---

## 3. Cash & finance intelligence

- **Direct-method 13-week cash forecast** (standard practice): opening bank + expected receipts (open invoices x per-party payment-timing distribution from section 1.4) - expected payments (open bills x our own pay behaviour, payroll calendar, GST/TDS due dates, EMIs) + recurring items detected from bank statement. Run **Monte Carlo** over payment-timing distributions to produce p10/p50/p90 cash paths and a "probability cash < Rs 0 in the next 4 weeks" runway alert. Survey context: 33.9% of small business owners have < 1 month of operating cash (Q1 2026 survey cited via Ramp guide). 
- **Working capital**: DSO, DPO, DIO, CCC each month with trend and peer band; Xero's Jun 2026 benchmarking labels ratios "leading, lagging, or in line with peers" at industry x country cohort level — copy that framing. QuickBooks exposes `benchmarking_against_industry` via its MCP tools. India-specific peer benchmarks do not exist as a public API; build them from own-tenant anonymised aggregates by NIC/HSN-derived industry once N >= 30 per cohort, and cite MSME Pulse macro figures until then.
- **GST liability forecast**: outward tax from sales forecast - eligible ITC from GSTR-2B (auto-generated on the 14th for the prior month; supplier non-filing means the invoice "simply doesn't appear" and ITC is not available — Section 16(2)(aa)/Rule 36(4)). Nudge: "Rs X of ITC at risk: 7 suppliers haven't filed GSTR-1".
- **TDS nudges**: deposit by 7th of next month (30 April for March), quarterly returns; interest 1.5%/month on late deposit and Rs 200/day late-filing fee under s.234E (standard practice; the ClearTax TDS page was unreachable at fetch time).
- **Loan/OD utilisation and idle cash**: from bank statements — flag OD utilisation > 80% for > 10 days, and idle balance > 2x monthly outflow p90 (suggest sweep/FD). Deterministic.
- **Ratio analysis with explanations**: compute current ratio, quick ratio, interest coverage, gross/net margin, inventory turns; LLM explains movement using the computed contributors (section 5's "top drivers" pattern).

---

## 4. Anomaly & integrity

| Check | Method | Library | False-positive control |
|---|---|---|---|
| Duplicate purchase invoice / payment | Probabilistic record linkage on (supplier, amount, date +-7d, invoice-no fuzzy, GSTIN) | **splink** (Fellegi-Sunter + EM, unsupervised, "a million records on a laptop in approximately one minute", DuckDB/Spark/Postgres backends, term-frequency adjustments); alt: **dedupe** (active learning, logistic regression on field distances), **recordlinkage** (ECM/FS, blocking, supervised) | Only surface pairs with match weight above a threshold calibrated on the tenant; auto-suppress pairs already marked "not duplicate" |
| OCR/keying variants ("O" vs "0") | Same as above with Jaro-Winkler on invoice number | splink comparison templates | — |
| Benford first/second-digit tests | Nigrini MAD conformity, chi-square, Z per digit | **pybenford / benfordpy** implement the Nigrini workflow with MAD classes | Use as screening only; "should not be used as a final decision-making tool"; fails on data with tight min/max or few entries |
| Round-number & threshold-avoidance | Share of amounts ending in 000/500; clustering just below Rs 50,000 (e-way bill threshold, intra-state varies Rs 50k-2 lakh), Rs 2 lakh cash limit, TDS section thresholds | SQL histograms + binomial test vs party baseline | Compare to the party's own historical round-number share |
| Outlier vouchers | ECOD/COPOD/IForest on (amount, hour, user, ledger, party) | **PyOD 3** (61 detectors; ADBench across 30 algorithms/57 datasets underpins its detector routing; ensembles IForest/LSCP/SUOD highlighted) | Report top-k per week, not all above score |
| Edit-log behavioural anomalies | Backdated edits, post-period alterations, deletions by user; user-level rate vs peers | SQL over audit log + z-score | Threshold on both count and value |
| Vendor master changes | Bank account / GSTIN / address changed then paid within N days | Rule | ACFE 2026: anti-fraud controls incl. "proactive data monitoring" are associated with lower losses and quicker detection; "unusually close vendor/customer relationships" median loss $300k |
| GST mismatches | HSN -> expected rate table vs invoice rate; GSTIN state code vs place of supply; GSTR-2B vs purchase register | Deterministic | Exact rules, no ML |
| Supplier non-filing | GSTR-2B absence, supplier filing frequency | Deterministic | — |

Alert-fatigue evidence: fraud transaction monitoring false positives 93% (2025) vs 97% (2015); mid-market banks 500-2,000 alerts/day at 85-95% FP; SOCs 9,854 FP/week (Ponemon via N-able); 73% of security teams name FPs their top challenge (SANS 2025). Design consequence: a weekly "things to check" digest capped at ~7 items, each with an expected-value score (probability x rupee impact), and a required feedback button whose answer trains suppression.

---

## 5. Turning data into words

### 5.1 How commercial products do it

| Product | Mechanism |
|---|---|
| **Tableau Pulse** | 13 deterministic insight types (period-over-period, correlated metrics, record-level outliers, forecast, current trend, trend-change alert, unexpected values, goal/threshold, pace-to-goal, top drivers, top detractors, concentrated-contribution alert, top/bottom contributors); ranked by analytic context, "statistical impact" score, and thumbs-up/down feedback; 2025 update improved seasonal expected-range detection |
| **ThoughtSpot SpotIQ** | z-score distribution measures; moving median for 5-30 points, Prophet for > 30 with 80/20 train/predict split and 2-cycle seasonality minimum; cross-correlations, linear-regression trends, k-means segments; usage-based ranking + reinforcement learning |
| **Google Analytics Insights** | Automated anomalies + up to 50 custom rule insights (hourly/daily/weekly/monthly; e.g., "30-day active users % decrease > 20"); "learns which ones you're most interested in and ranks your new insights accordingly" |
| **Zoho Books Zia** | Ask Zia, CoCreate (NL -> quote/SO/invoice), report forecast + "View Anomaly", invoice-agent insights (overdue balances, payment-delay patterns, invoices exceeding the org's average late days), bank-statement categorisation, Zia Insights on reports |
| **Power BI Copilot** | Report/semantic-model summaries, narrative visual, Q&A; explicitly requires "prepare your data for AI" (descriptions, verified answers, AI instructions) else it "can misinterpret the data"; 10,000-char prompts; answers from the semantic model when relevant, otherwise LLM general knowledge; not yet available in India West |
| **Databricks Genie** | Governed via Unity Catalog; Genie Agents where teams "configure trusted data, metrics, and business rules"; verified answers, sample queries, instructions |
| **Snowflake Cortex Analyst** | Semantic Views/YAML (logical tables, dimensions, facts, metrics, relationships, synonyms), verified query repository, predefined join paths, multi-turn |
| **Amazon Q in QuickSight** | Executive summaries, data stories, Q&A Topics as the semantic layer, "custom instructions" to improve accuracy; Bedrock-based |
| **Narrative BI** | Proactive, dashboard-less narrative insights; team acquired by Cube (Sep 9, 2025) to build "agentic workflows that proactively surface insights" on top of the semantic layer |
| **Vanna 2.0** | User-aware agent + tool registry with permission checks, streams SQL/tables/Plotly; Postgres etc. |
| **WrenAI** | MDL semantic layer (models, relationships, metrics, "Git-friendly, reviewable"), Wren Engine on Apache DataFusion (22+ sources), dry-plan validation, structured error hints |

### 5.2 Text-to-SQL state of the art (Sept 2026)

- **BIRD**: human 92.96%; top submissions ~82% test EX (SiriusAI-SQL 82.28%, Sep 2 2026; DataGallery 82.22%; AskData+GPT-4o 81.95%; Agentar-Scale-SQL 81.67%), all with oracle knowledge. Arctic-Text2SQL-R1-32B (May 2025) was 71.83%.
- **Spider 2.0** (632 enterprise workflow problems, 1,000+ column DBs, SQL often > 100 lines): Lite top 76.23%, Snow top 96.70% (agents), DBT top 65.6%; GPT-4o baseline was 10.1% vs 86.6% on Spider 1.0. Ongoing Snow leaderboard disruptions due to evaluation-account suspension.
- **Semantic layer evidence**: Cube +17.2/+22.2/+23.2 pp (Opus 4.7 50.5 -> 67.7; Sonnet 4.6 46.5 -> 68.7; GPT-5.4 45.5 -> 68.7; McNemar p <= 0.0015, n = 99, Apr 28 2026); dbt Labs (Apr 7 2026; 11 questions x 20 runs): Sonnet 4.6 90.0 -> 98.2, GPT-5.3-Codex 84.1 -> 100; SL fails loudly, text-to-SQL fails silently; adding 3 dbt models improved *both* approaches; questions "requiring too many entity hops" scored 0% via SL — so keep an escape hatch to governed SQL.

### 5.3 Insight ranking ("what to surface")

Interestingness literature (Geng & Hamilton, ACM Computing Surveys 2006 — fetch blocked, cited from knowledge) frames nine criteria: conciseness, generality, reliability, peculiarity, diversity, novelty, surprisingness, utility, actionability. Practical scoring used by Pulse/SpotIQ/GA reduces to: (a) **surprise** = |actual - expected| / expected-range width from a seasonal baseline; (b) **significance** = test vs baseline noise (z or bootstrap), min data (ThoughtSpot: >= 5 points; 2 seasonal cycles); (c) **impact** = rupee magnitude; (d) **actionability** = does an action exist; (e) **novelty** = not shown in the last N days; (f) **user affinity** learned from feedback. Score = impact x P(real) x actionability x novelty x affinity.

---

## 6. Proactive / ambient agents

- **Triggers**: Postgres `NOTIFY` (payload <= 8000 bytes, delivered on commit, deduped per transaction, 8 GB queue, `pg_notification_queue_usage()` monitoring, not 2PC-compatible) for in-process reactions; Debezium/pgoutput CDC when events must fan out to the Python plane durably (not fetched; standard practice); **Temporal Schedules** (interval or calendar specs, jitter, phase offset, overlap policies Skip/BufferOne/BufferAll/CancelOther/TerminateOther/AllowAll, pause, backfill) for the nightly feature build and the morning digest.
- **Morning digest**: one message, <= 5 items, ordered by score, each with a one-tap action; India-specific channel is WhatsApp (Khatabook-style reminders are the norm for small merchants; fetch returned no content, so no numbers claimed).
- **Personalisation**: Duolingo's Recovering Difference Softmax — per-arm score = weighted-importance-sampled relative lift when used vs eligible, empirical-Bayes shrinkage for small arms, recency penalty gamma x 0.5^(d/h) for novelty decay, softmax with temperature tau; deployed to millions of daily reminders; +0.5% DAU, +2% new-user retention. Same structure fits "which of 8 digest templates / which hour" per owner.
- **Fatigue control**: per-user daily cap, quiet hours, escalating only on rupee impact, and every notification carrying a "less of this" control that feeds suppression (GA and Pulse both do feedback-based ranking).

---

## 7. Explainability & trust for numeric insights

1. **Compute deterministically, narrate with the LLM** — Tableau's "facts ... guaranteed to be accurate" pattern; dbt's finding that the SL path never silently returns wrong data.
2. **Program-of-Thought / tool arithmetic**: PoT (arXiv 2211.12588) delegates computation to executed code and gains ~12% average over chain-of-thought across GSM8K/AQuA/SVAMP/TabMWP/MultiArith and FinQA/ConvFinQA/TAT-QA. Never let the model add numbers in prose; the numbers come from a query result object and the narration template references them by key.
3. **Drill-down to vouchers**: every insight carries the SQL/DSL and the voucher IDs behind each number; the UI opens the ledger. Tally users trust what they can F5 into.
4. **Confidence**: quantile forecasts (TimesFM 10-90th pct; conformal intervals in HierarchicalForecast), survival CIs, Bayesian posteriors (PyMC-Marketing CLV).
5. **What-if**: deterministic simulators (change credit days -> DSO/cash path; change reorder point -> stock-out probability) with LLM only translating the request into parameters.
6. **SHAP on GBDTs** for late-payment and churn scores (the Hilti thesis used SHAP to confirm "intuitive drivers" for governance and user trust).
7. **Verified answers**: keep a tenant-independent library of question -> DSL pairs (Cortex Analyst's verified queries, Power BI verified answers, Genie sample queries) and prefer them over free generation.

---

## 8. Recommendation

### 8.1 Insight catalogue (52 types)

Legend — Cadence: RT = on event, D = daily, W = weekly, M = monthly. Action column = what the agent offers, always behind approval.

**A. Daily ops reminders**
| # | Insight | Data | Algorithm | Cadence | Action |
|---|---|---|---|---|---|
| 1 | Invoices due today/tomorrow | Sales bills, terms | Rule | D | Send reminder (WhatsApp/email) |
| 2 | Bills we owe due in 3 days | Purchase bills | Rule + cash forecast | D | Schedule payment / prioritise |
| 3 | Cheques/PDCs maturing | Bank vouchers | Rule | D | Confirm deposit |
| 4 | Pending sales orders past promised date | Orders vs deliveries | Rule | D | Draft delivery note / call list |
| 5 | Unreconciled bank lines > 3 days | Bank statement vs books | Match engine residuals | D | Suggest matches |
| 6 | Unposted/draft vouchers | Voucher status | Rule | D | Open queue |
| 7 | E-way bill needed for today's dispatches > Rs 50k | Invoice value, state threshold | Rule | RT | Generate e-way bill |

**B. Receivables**
| # | Insight | Data | Algorithm | Cadence | Action |
|---|---|---|---|---|---|
| 8 | Predicted-late invoices (P > 0.5) | Party history, invoice | LightGBM + SHAP | D | Pre-emptive reminder |
| 9 | Expected payment date per invoice (p50/p80) | Same | Survival (RSF/GBS) | D | Update cash forecast |
| 10 | Customer paying slower than their own norm | Days-to-pay trend | CUSUM / z vs party baseline | W | Call / tighten terms |
| 11 | Credit limit breach / exposure > limit | Outstanding + limit | Rule | RT | Hold order / request approval |
| 12 | Concentration risk (top 3 debtors > 50% AR) | AR ageing | Rule | M | Diversify / insure |
| 13 | Best time to remind this customer | Reminder outcomes | Bandit | D | Schedule reminder |
| 14 | Disputed / short-paid patterns | Credit notes, partial receipts | Rule + rate test | W | Investigate item/pricing |

**C. Payables**
| # | Insight | Data | Algorithm | Cadence | Action |
|---|---|---|---|---|---|
| 15 | Early-payment discount worth taking | Bill terms, cash forecast | NPV rule | D | Pay early |
| 16 | Duplicate supplier invoice suspected | Purchase bills | splink linkage | RT | Hold payment |
| 17 | Supplier price creep on item | Purchase rates | Rolling p90 test | M | Renegotiate / alt supplier |
| 18 | Supplier hasn't filed GSTR-1 (ITC at risk) | GSTR-2B vs register | Deterministic | M (15th) | Send nudge to supplier |
| 19 | Vendor bank details changed before payment | Party master audit | Rule | RT | Verify by call |
| 20 | Paying suppliers faster than customers pay us (DPO < DSO) | Ledgers | Ratio | M | Renegotiate terms |

**D. Sales growth / cross-sell**
| # | Insight | Data | Algorithm | Cadence | Action |
|---|---|---|---|---|---|
| 21 | Customers to push item X to | Line items | implicit ALS + rule lift | W | Draft offer message list |
| 22 | Complementary items missing from a customer's basket | Line items | FP-growth rules | W | Suggest add-ons on next order |
| 23 | Customers overdue for reorder vs cadence | Inter-order gaps | BG/NBD P(alive) / p90 gap | D | Reorder prompt |
| 24 | Churn-risk customers (declining frequency/value) | RFM trend | Slope + P(alive) | W | Retention call |
| 25 | Top-CLV customers and their share | BG/NBD + Gamma-Gamma | Bayesian | M | Account priority |
| 26 | Under-priced customer-item pairs (below margin floor) | Rate, cost | Margin calc | M | Reprice |
| 27 | Festival demand uplift expected (per item/segment) | History + holidays covariates | Forecast with covariates | M (T-45d) | Pre-book stock / campaign |
| 28 | Region/state where sales are dropping | Party state | Period-over-period + significance | M | Field follow-up |
| 29 | New-customer acquisition trend | First-invoice dates | Trend | M | — |

**E. Inventory**
| # | Insight | Data | Algorithm | Cadence | Action |
|---|---|---|---|---|---|
| 30 | Reorder now (on-hand < ROP) | Stock, forecast, lead time | Quantile forecast + ROP | D | Draft PO |
| 31 | Stock-out risk in next 14 days | Same | P(demand > available) | D | Expedite |
| 32 | Dead stock (no movement N days) | Movements | Rule | M | Discount / return |
| 33 | Expiring batches vs sell-through | Batch expiry + forecast | Rule | W | Promote / return |
| 34 | ABC-XYZ reclassification | Value + CV | Deterministic | M | Policy change |
| 35 | Overstock (days of cover > threshold) | Stock/forecast | Rule | W | Pause POs |
| 36 | Supplier lead-time drift | PO -> GRN | Distribution shift test | M | Buffer / alt supplier |
| 37 | Godown imbalance (stock-out in A, excess in B) | Godown stock | Rule | W | Transfer note |
| 38 | Negative stock / valuation anomalies | Stock ledger | Rule | D | Fix entries |

**F. Cash**
| # | Insight | Data | Algorithm | Cadence | Action |
|---|---|---|---|---|---|
| 39 | 13-week cash path p10/p50/p90 | AR/AP timing dists, recurring | Monte Carlo | W | — |
| 40 | Runway / shortfall alert (P(cash < 0) > 20%) | Same | Same | D | Accelerate collections list |
| 41 | OD utilisation > 80% sustained | Bank | Rule | W | Sweep / renegotiate |
| 42 | Idle cash above buffer | Bank | Rule | W | FD / sweep suggestion |
| 43 | Recurring outflow increased (rent, EMI, subscriptions) | Bank statement clustering | Recurrence detection | M | Review |

**G. Tax / compliance**
| # | Insight | Data | Algorithm | Cadence | Action |
|---|---|---|---|---|---|
| 44 | GST liability estimate for the month + ITC available | Sales, GSTR-2B | Deterministic + forecast | M (from 14th) | Reserve cash |
| 45 | HSN-rate mismatch on invoice | HSN table | Rule | RT | Correct invoice |
| 46 | Place-of-supply / GSTIN state mismatch | Party GSTIN, invoice | Rule | RT | Correct |
| 47 | TDS deducted but not deposited by 7th | TDS ledgers | Rule | M | Pay |
| 48 | Purchase register vs GSTR-2B mismatch list | Both | Matching | M | Contact supplier |

**H. Integrity**
| # | Insight | Data | Algorithm | Cadence | Action |
|---|---|---|---|---|---|
| 49 | Backdated / post-close edits by user | Audit log | Rule + rate | W | Review |
| 50 | Amount outliers by ledger/user | Vouchers | PyOD ECOD/IForest | W | Review top-k |
| 51 | Threshold-clustering (just below Rs 50k / 2 lakh) | Amounts | Histogram test | M | Review |
| 52 | Benford deviation on expense ledgers | Amounts | Nigrini MAD | Q | Deeper audit (screening only) |

**I. People / payroll** (add-on): salary cost % of revenue trend; PF/ESI due nudges; overtime spikes; headcount vs sales productivity — rules and period-over-period.

**J. Benchmarking** (add-on): DSO/DPO/DIO/gross margin vs anonymised same-industry tenants ("leading / lagging / in line", Xero framing) once cohort N >= 30; until then vs own trailing 12 months.

### 8.2 Insight-engine architecture

```
Postgres 16 (tenant RLS)
  |-- source tables (vouchers, lines, parties, stock, bank, gst)
  |-- NOTIFY on voucher commit  --> Go event router --> RT rules (7,11,16,19,45,46)
  |-- features schema (per tenant, per entity, per as_of_date)
  |     party_features(party_id, as_of, rfm_*, dtp_p50, dtp_p90, p_alive, exposure, ...)
  |     item_features(item_id, godown_id, as_of, abc, xyz, rop, forecast_p50/p90[h], days_cover)
  |     tenant_features(as_of, dso, dpo, dio, cash_p10/50/90[w], gst_liab_est)
  |-- insights(id, tenant, type, entity, score, impact_inr, p_real, payload_json, evidence_sql, voucher_ids[], created_at, expires_at, state)
  |-- insight_feedback(insight_id, user, verdict, ts)      -- trains suppression + affinity
  |-- insight_suppression(tenant, type, entity, until, reason)
Python AI plane (Fireworks open-weight LLM for narration only)
  |-- nightly Temporal schedule: build features (StatsForecast/Chronos-2, LightGBM, PyMC-Marketing, splink, PyOD)
  |-- detectors emit candidate insights with (impact, p_real, actionability)
  |-- ranker: score = impact x p_real x actionability x novelty(type,entity) x affinity(user,type)
  |-- dedupe: same (type, entity) within TTL merges; contradictory insights resolved by higher p_real
  |-- digest composer: top-5 per user per morning; LLM narrates from payload_json via PoT-style templating (numbers by key only)
  |-- semantic layer (metric definitions + verified DSL) used by NL Q&A and by narration to name metrics consistently
Feedback loop
  |-- verdicts -> per-type precision estimate per tenant; auto-lower cadence of types with precision < 0.5
  |-- bandit over digest templates/timing (Recovering Difference Softmax style)
```

Key build rules: (1) every insight row stores the evidence query and voucher IDs; (2) no LLM call may produce a number that is not in payload_json; (3) all thresholds are tenant-relative (own history) before being absolute; (4) hard cap on digest size and a per-type weekly cap; (5) foundation-model forecasts are opt-in per tenant after backtest beats the classical ensemble on their data; (6) license check: Chronos-2/TimesFM-2.5 (Apache-2.0) yes, Moirai-2 (CC-BY-NC) no.

---

## Sources (fetched or seen in search results, 2026-09-08)

1. https://cube.dev/blog/why-semantic-layers-make-llm-analytics-reliable-a-paired-benchmark-across-three-frontier-models
2. https://github.com/cubedevinc/semantic-layer-benchmark
3. https://docs.getdbt.com/blog/semantic-layer-vs-text-to-sql-2026
4. https://dbt-labs.github.io/dbt-llm-sl-bench/
5. https://arxiv.org/pdf/2604.25149 (Semantic Layers for Reliable LLM-Powered Data Analytics)
6. https://help.tableau.com/current/online/en-us/pulse_insights_platform_insight_types.htm
7. https://help.tableau.com/current/online/en-us/pulse_alerts.htm
8. https://www.tableau.com/blog/top-new-tableau-pulse-feature-releases-know
9. https://docs.thoughtspot.com/cloud/26.8.0.cl/time-series-anomaly
10. https://www.thoughtspot.com/product/analytics/spotiq
11. https://support.google.com/analytics/answer/9443595
12. https://www.zoho.com/us/books/help/ai-features/ai-features.html
13. https://learn.microsoft.com/en-us/power-bi/create-reports/copilot-introduction
14. https://docs.databricks.com/aws/en/genie/
15. https://docs.snowflake.com/en/user-guide/snowflake-cortex/cortex-analyst
16. https://docs.aws.amazon.com/quicksight/latest/user/quicksight-gen-bi.html
17. https://cube.dev/blog/building-the-future-of-agentic-analytics-narrative-bi-team-joins-cube
18. https://github.com/vanna-ai/vanna
19. https://github.com/Canner/WrenAI
20. https://bird-bench.github.io/
21. https://spider2-sql.github.io/
22. https://github.com/xlang-ai/Spider2
23. https://www.snowflake.com/en/blog/engineering/arctic-text2sql-r1-sql-generation-benchmark/
24. https://arxiv.org/abs/2211.12588 (Program of Thoughts)
25. https://www.diva-portal.org/smash/get/diva2:1987611/FULLTEXT01.pdf (Pay Me Maybe, KTH/Hilti 2025)
26. https://arxiv.org/abs/1912.10828 (Optimize Cash Collection)
27. https://www.sciencedirect.com/science/article/pii/S2666827024000549 (framework for invoice payment predictions)
28. https://www.semanticscholar.org/paper/Modelling-late-invoice-payment-times-using-survival-Smirnov/2c8aa2d189d22ebd171474cb0502c263643e8bd5
29. https://quickbooks.intuit.com/r/small-business-data/small-business-late-payments-report-2026/
30. https://quickbooks.intuit.com/payments-agent/
31. https://quickbooks.intuit.com/learn-support/en-us/help-article/intuit-assist/get-started-payments-agent/L0FyKj9NL_US_en_US
32. https://www.tesorio.com/blog/ai-improves-customer-payment-prediction
33. https://www.chaserhq.com/blog/upflow-alternatives
34. https://yaypay.helpjuice.com/payments/payment-projections-cash-forecasting
35. https://www.bluecopa.com/blog/ai-collection-software (Growfin 94% claim)
36. https://www.growfin.ai/blog/guide-to-ar-automation-technologies-benefits-and-best-practices
37. https://www.kredx.com/
38. https://www.sidbi.in/msme-pulse
39. https://www.xero.com/us/media-releases/xero-introduces-industry-benchmarking-intelligence-for-small-businesses/
40. https://www.xero.com/us/media-releases/us-xsbi-march-quarter/
41. https://www.pymc-marketing.io/en/stable/notebooks/clv/clv_quickstart.html
42. https://www.pymc-marketing.io/en/0.15.1/notebooks/clv/bg_nbd.html
43. https://lifelines.readthedocs.io/en/latest/
44. https://scikit-survival.readthedocs.io/en/stable/user_guide/index.html
45. https://rasbt.github.io/mlxtend/user_guide/frequent_patterns/fpgrowth/
46. https://github.com/benfred/implicit
47. https://www.sciencedirect.com/science/article/pii/S2667305325000031 (hybrid ARM + i-ALS + SVD++ B2B distributor)
48. https://www.stepbystepdatascience.com/hybrid-recommender-lightfm-python
49. https://github.com/Nixtla/statsforecast
50. https://www.nixtla.io/docs/use_cases/forecasting_intermittent_demand
51. https://github.com/Nixtla/hierarchicalforecast
52. https://arxiv.org/abs/2510.15821 (Chronos-2)
53. https://huggingface.co/google/timesfm-2.5-200m-pytorch
54. https://huggingface.co/Salesforce/moirai-2.0-R-small
55. https://huggingface.co/spaces/Salesforce/GIFT-Eval
56. https://machinelearningmastery.com/the-2026-time-series-toolkit-5-foundation-models-for-autonomous-forecasting/
57. https://arxiv.org/pdf/2605.24381 (Operational viability of TS foundation models)
58. https://arxiv.org/abs/2304.03092 (Combining probabilistic forecasts of intermittent demand)
59. https://www.sciencedirect.com/science/article/abs/pii/S0169207022000577 (M5 conclusions)
60. https://www.semanticscholar.org/paper/The-M5-Accuracy-competition:-Results,-%EF%AC%81ndings-and-Makridakis-Spiliotis/bc64d63e0f4fc159acb3cb0cade01c12d2213914
61. https://github.com/vacanza/holidays and https://raw.githubusercontent.com/vacanza/holidays/dev/holidays/countries/india.py
62. https://facebook.github.io/prophet/docs/seasonality,_holiday_effects,_and_regressors.html
63. https://moj-analytical-services.github.io/splink/
64. https://github.com/dedupeio/dedupe
65. https://recordlinkage.readthedocs.io/en/latest/
66. https://github.com/yzhao062/pyod
67. https://github.com/anirbanmukherjee2709/pybenford
68. https://www.ncbi.nlm.nih.gov/pmc/articles/PMC8720889/ (Benford limitations)
69. https://www.acfe.com/fraud-resources/report-to-the-nations
70. https://cleartax.in/s/gstr-2b
71. https://cleartax.in/s/eway-bill-gst-rules-compliance
72. https://www.fluxforce.ai/blog/fraud-alert-fatigue (93% FP in transaction monitoring)
73. https://www.n-able.com/blog/alert-fatigue-cybersecurity (Ponemon 9,854 FP/week)
74. https://dl.acm.org/doi/10.1145/3723158 (Alert fatigue in SOCs, ACM CSUR)
75. https://research.duolingo.com/papers/yancey.kdd20.pdf (Recovering Difference Softmax)
76. https://www.postgresql.org/docs/16/sql-notify.html
77. https://docs.temporal.io/schedule
78. https://ramp.com/blog/cash-flow-forecasting (33.9% < 1 month cash, Q1 2026 survey)
79. https://arxiv.org/pdf/2201.03367 (account-level Monte Carlo debt recovery)
80. https://www.stampli.com/resources/duplicate-invoice-detection/

Not reachable at fetch time (claims from these are marked standard practice or omitted): Debezium Postgres connector docs (403), Geng & Hamilton ACM survey (403), microprediction Prophet critique (404 after redirects), Amplitude anomaly docs (404), ClearTax TDS due-dates pages (404), Khatabook homepage (empty).
