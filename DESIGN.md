---
name: Tallyweb
description: Shared tokens for keyboard-first accounting and public web surfaces.
colors:
  canvas: "rgb(10 30 94)"
  panel: "rgb(19 40 122)"
  panel-hover: "rgb(27 53 144)"
  ink: "rgb(0 0 128)"
  text: "rgb(255 255 255)"
  muted: "rgb(185 201 238)"
  accent: "rgb(255 210 77)"
  accent-hover: "rgb(255 222 126)"
  success: "rgb(31 157 85)"
  success-text: "rgb(143 231 177)"
  danger: "rgb(179 0 0)"
  danger-text: "rgb(255 180 173)"
  bar: "rgb(201 212 255)"
  border: "rgb(61 83 145)"
  paper: "rgb(247 248 253)"
  paper-muted: "rgb(82 99 143)"
typography:
  display:
    fontFamily: "\"IBM Plex Sans\", \"Segoe UI\", sans-serif"
    fontSize: "clamp(2.75rem, 5.1vw, 4.75rem)"
    fontWeight: 500
    lineHeight: 1.08
    letterSpacing: "-0.035em"
  headline:
    fontFamily: "\"IBM Plex Sans\", \"Segoe UI\", sans-serif"
    fontSize: "clamp(2rem, 3.1vw, 2.875rem)"
    fontWeight: 500
    lineHeight: 1.16
    letterSpacing: "-0.035em"
  body:
    fontFamily: "\"IBM Plex Sans\", \"Segoe UI\", sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.6
  button:
    fontFamily: "\"IBM Plex Sans\", \"Segoe UI\", sans-serif"
    fontSize: "0.875rem"
    fontWeight: 600
    lineHeight: 1.4
  data:
    fontFamily: "ui-monospace, \"SFMono-Regular\", Menlo, Consolas, \"Liberation Mono\", monospace"
  keycap:
    fontFamily: "ui-monospace, \"SFMono-Regular\", Menlo, Consolas, \"Liberation Mono\", monospace"
    fontSize: "0.75rem"
    fontWeight: 500
    lineHeight: 1.3
rounded:
  "sm": "0.25rem"
  "md": "0.5rem"
  "lg": "0.75rem"
spacing:
  "1": "0.25rem"
  "2": "0.5rem"
  "3": "0.75rem"
  "4": "1rem"
  "5": "1.25rem"
  "6": "1.5rem"
  "8": "2rem"
  "10": "2.5rem"
  "12": "3rem"
  "16": "4rem"
  "20": "5rem"
  "24": "6rem"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.canvas}"
    typography: "{typography.button}"
    rounded: "{rounded.sm}"
    padding: "0.75rem 1.25rem"
  button-primary-hover:
    backgroundColor: "{colors.accent-hover}"
  button-secondary:
    backgroundColor: "transparent"
    textColor: "{colors.text}"
    typography: "{typography.button}"
    rounded: "{rounded.sm}"
    padding: "0.75rem 1.25rem"
  button-secondary-hover:
    backgroundColor: "{colors.panel}"
  button-quiet:
    backgroundColor: "transparent"
    textColor: "inherit"
    typography: "{typography.button}"
    rounded: "{rounded.sm}"
    padding: "0.75rem 1.25rem"
  button-quiet-hover:
    backgroundColor: "rgb(201 212 255 / 0.1)"
  input:
    backgroundColor: "{colors.text}"
    textColor: "{colors.ink}"
    rounded: "{rounded.sm}"
    padding: "{spacing.3}"
    width: "100%"
  keycap:
    typography: "{typography.keycap}"
    rounded: "{rounded.sm}"
    padding: "0.25rem 0.5rem"
  review-card:
    backgroundColor: "{colors.text}"
    textColor: "{colors.canvas}"
    rounded: "{rounded.md}"
  alert:
    backgroundColor: "{colors.text}"
    textColor: "{colors.danger}"
    rounded: "{rounded.sm}"
    padding: "0.75rem 1rem"
---

# Design System: Tallyweb

## Overview

**Creative North Star: "Tally-style accounting"**

The existing accounting UI supplies the identity: deep blue, yellow selections, pale function-key bars, and precise monospace data. Public pages extend that identity with more generous spacing and IBM Plex Sans. The workspace keeps its compact keyboard-driven layout.

The runtime source of truth is `frontend/src/design-system/tokens.css`. Reusable primitives live in `frontend/src/design-system/components.tsx` and `components.css`. Update this reference and `.impeccable/design.json` when the implementation changes. Sidecar tonal ramps are generated swatch aids, not additional runtime colors.

**Key Characteristics:**
- Deep blue surfaces with yellow actions and selections.
- Sans-serif public copy paired with precise monospace financial data.
- Compact workspace density and generous public-page spacing.
- Shared native controls with visible keyboard focus.

## Colors

The palette carries the existing blue-and-yellow accounting identity across dark workspace surfaces and pale public sections. Frontmatter preserves the runtime RGB channels as valid CSS colors.

### Primary

`accent` marks primary actions and selections; `accent-hover` is the lighter primary-button hover fill. `canvas` and `panel` establish the blue backdrop and layered regions. `panel-hover` is declared in the runtime scale; the current secondary-button hover uses `panel`.

### Neutral

Use `text` and `muted` for primary and secondary content on blue. On pale `paper` surfaces, use `canvas` for headings and `paper-muted` for body copy. `ink` is the data-entry text color. `bar` supplies pale function-key strips; `border` supplies dividers and strokes, sometimes with reduced opacity.

`success` supplies existing filled states; `success-text` is the light positive-status color on blue. `danger` appears in errors on white and shortcut labels on pale bars; `danger-text` is the light error color and invalid-input stroke on blue.

**The Semantic Color Rule.** Reuse the shared roles and preserve their surface context; pair status color with text or another non-color signal.

Colors are stored as RGB channels so opacity remains usable in Tailwind:

```css
.example {
  background: rgb(var(--tw-color-panel));
  border: var(--tw-border-width) solid rgb(var(--tw-color-border) / 0.5);
  padding: var(--tw-space-6);
}
```

Existing `bg-tally-bg`, `bg-tally-panel`, `text-tally-title`, `text-tally-field`, and other `tally-*` utilities resolve to the same tokens. Their original values are preserved. Compatibility names such as `barKey` and `fieldText` retain their established meaning.

## Typography

IBM Plex Sans is self-hosted in `frontend/public/fonts` with its license and `font-display: swap`. Full fallback stacks are in the frontmatter. Use `--tw-font-sans` for public copy, forms, and navigation; use `--tw-font-mono` for the accounting workspace, shortcuts, codes, and numerical data.

The `display` role is the desktop hero; `headline` is public section headings. The shared size scale runs from `--tw-text-xs` through `--tw-text-2xl`, plus `--tw-text-display`. Body copy uses `--tw-leading-body`; display headings use `--tw-leading-tight` and `--tw-tracking-display`. Public paragraphs are constrained to 65ch, with narrower introductory measures.

At the public mobile breakpoint, the hero uses `clamp(2.75rem, 9.5vw, 4.25rem)`. Feature titles change from 1.5rem to 1.25rem. The sign-in heading uses 2.5rem, weight 500, and line-height 1.15. These are surface-specific treatments.

**The Financial Data Rule.** Keep financial values in the monospace data treatment, use tabular numerals, and right-align amount columns. Data sizes follow each screen’s density rather than a single global size.

## Layout

The space scale follows a four-pixel base and is available as `--tw-space-*` and existing Tailwind spacing utilities. Public content uses `--tw-content-width` (76rem) and `--tw-page-gutter` (`clamp(1.25rem, 4vw, 3rem)`). Small gaps connect related controls; larger gaps separate sections.

Public layouts tighten gaps at 1050px and stack columns at 760px. At that mobile breakpoint, in-page header navigation is hidden while the brand and sign-in action remain; review-section copy moves above its example. A 380px adjustment reduces brand and illustrative-preview density. Sign-in has a separate 480px adjustment for page and form padding; its main column is capped at 26rem. These are CSS maximum-width breakpoints, not Tailwind's default screen aliases.

**The Density Rule.** Preserve the accounting character grid when reusing public-page primitives; public spacing and heading sizes do not define workspace density.

## Elevation & Depth

Most hierarchy comes from blue surface layers, pale sections, and dividers. `--tw-shadow-preview` (`0 24px 64px rgb(2 12 45 / 0.3)`) gives the product example its single lift. The sign-in panel and document-review example remain flat.

**The Surface Layer Rule.** Use the existing surface and divider roles for ordinary grouping; reserve the preview shadow for the lifted product example.

## Shapes

Controls and keycaps use `--tw-radius-sm`; panels and previews use `--tw-radius-md`. The larger `--tw-radius-lg` remains available in the runtime scale. Dividers use `--tw-border-width` (1px). The tally-mark SVG is the shared brand asset for headers, README branding, and the favicon.

## Components

### Buttons

Import primitives from `frontend/src/design-system/components.tsx`. `Button` and `ButtonLink` support `primary`, `secondary`, and `quiet`. Primary uses yellow with dark-blue text. Secondary uses white text and a blue border on blue surfaces; hover fills with `panel` and changes the border to `muted`. Quiet inherits its text color and adds a translucent `bar` fill on hover.

Controls have a minimum height of `--tw-control-height` (2.875rem), not a fixed height. Disabled buttons use opacity 0.55 and a not-allowed cursor. The background and border transitions use `--tw-duration-fast` (160ms). Anchors remain anchors and actions remain buttons.

```tsx
import { Brand, Button, ButtonLink, Keycap } from "./design-system/components";

<Brand />
<Button type="submit" disabled={busy} aria-busy={busy}>Save</Button>
<Button variant="secondary" onClick={cancel}>Cancel</Button>
<ButtonLink href="/signin">Open workspace</ButtonLink>
<Keycap>F8</Keycap>
```

### Inputs and alerts

`tw-field`, `tw-input`, and `tw-alert` provide reusable form styles without hiding native labels, input attributes, or error semantics. Inputs use white fill, ink text and caret, a border stroke, and the shared minimum control height. Invalid fields use `danger-text` for the border. Disabled inputs use opacity 0.65 and a not-allowed cursor. Alerts use `danger` text on white. Preserve the sign-in form's distinct invalid-credential, lockout, and service-error messages.

```tsx
<label className="tw-field">
  <span>User name</span>
  <input className="tw-input" autoComplete="username"
    aria-invalid={hasError || undefined}
    aria-describedby={hasError ? "field-error" : undefined} />
</label>
{hasError && <p id="field-error" role="alert" className="tw-alert">Check your user name.</p>}
```

### Navigation, keycaps, and containers

`Brand` shares the tally-mark asset, sans wordmark, and yellow dot; its compact variant shows only the mark. Public navigation uses small sans text, `muted` at rest and `accent` on hover. `Keycap` uses a monospace label inside a current-color border. Keycaps explain shortcuts; they are not interactive chips.

The sign-in form groups controls on a blue `panel` with medium corners and space-8 padding, reduced to space-6 on small screens. The document-review example is a white, medium-cornered container with a translucent border and internal dividers, without a shadow. Feature descriptions use divided rows rather than individual cards.

### Focus and motion

The global focus ring uses `--tw-focus-color`, `--tw-focus-width` (2px), and `--tw-focus-offset` (4px). Light sections set `--tw-focus-color: var(--tw-color-ink)`. Preview tabs and their content use an inset outline offset (-4px). Preview tabs support Left/Right, Home/End, and normal Tab navigation. FAQ disclosures use native `details` and `summary` elements.

The preview enters with a 12px upward translation over `--tw-duration-enter` (600ms), using `--tw-ease-out` (`cubic-bezier(0.16, 1, 0.3, 1)`). Content is visible by default. Under `prefers-reduced-motion: reduce`, both shared durations resolve to 0ms.

**The Native Control Rule.** Preserve native links, buttons, labels, form submission, and disclosures; retain visible keyboard focus and expose loading, disabled, and error states through the existing semantics.

## Do's and Don'ts

### Do:
- Do reuse the shared CSS tokens and existing Tailwind compatibility aliases.
- Do use yellow for primary actions and selected accounting rows.
- Do pair public sans-serif copy with monospace financial data and shortcut labels.
- Do preserve native control semantics, visible focus, and reduced-motion behavior.
- Do preserve the compact accounting workspace when extending the public design system.

### Don't:
- Don't introduce independent color literals in feature components when a shared role exists.
- Don't communicate status through color alone.
- Don't remove a keyboard focus indicator.
- Don't add the preview shadow to every feature or nest bordered cards inside cards.
- Don't replace specific authentication failures with a generic error message.
