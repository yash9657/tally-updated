# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Product Purpose

Tallyweb is a browser-based accounting and ERP application. Its implemented workflows include voucher entry, financial reports, inventory, bank reconciliation, tax reports, document-to-draft processing, and AI-assisted review.

## Users

Repository documentation identifies Indian businesses and their accountants as the intended audience. The application is built around familiar Tally-style keyboard navigation. No customer counts, testimonials, or performance benchmarks have been supplied.

## Capabilities and Constraints

The public home page introduces the product. Sign-in is at `/signin`; the authenticated accounting workspace is at `/app`. Access uses an existing user name and password; there is no self-service registration flow. The browser accesses the accounting core through same-origin `/auth` and `/v1` APIs.

AI and statutory capabilities depend on deployed service configuration. Product copy must not promise production certification, automatic statutory filing, or unsupported integrations. Sample financial data in the landing preview is explicitly illustrative.

## Brand Commitments

The user requested that the landing page match the existing Tally-style UI and that design tokens be reusable throughout the app. The product name for this work is Tallyweb. Tallyweb is an independent project, not a Tally Solutions product.

## Evidence on Hand

The current frontend, its keyboard and screen registries, service documentation, and the site at https://tallyweb.world/ establish the implemented experience. The root README provides developer setup. Historical handoffs are reference evidence, not the current product specification.
