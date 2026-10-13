import { describe, expect, it } from "vitest";
import surface from "../api/goldens/print-template-surface.json";
import {
  PRINT_TEMPLATE_FIELDS,
  PRINT_TEMPLATE_FIELD_LABELS,
  PRINT_TEMPLATE_KINDS,
  printTemplateHonours,
  printTemplateKindLabel,
  printTemplateSurfaceFor,
} from "./printTemplateSurface";

/**
 * THE COUNTERWEIGHT TO A GENERATED GOLDEN — §0.1 item 4's rule, applied.
 *
 * `print-template-surface.json` is emitted by `internal/printing`'s
 * `TestPrintTemplateSurfaceGolden`, which renders each document kind twice per
 * field and compares the PDF bytes. That generator is stronger than
 * `printed-report-layouts.json`'s in one specific way — it needs NO Postgres, so
 * it cannot skip — but it is still a Go test, and this repository has been caught
 * by a Go test whose skip was indistinguishable from a pass.
 *
 * ⛔ SO THESE ASSERT THE ARTEFACT'S SHAPE IN A SUITE THAT ALWAYS RUNS. A golden
 * that went empty, went uniform, or lost its ragged grid would turn every
 * consumer in `printTemplateSurface.ts` into a silent no-op — the editing form
 * would offer no controls, or would offer all seven for every kind including the
 * one where four of them never print.
 *
 * ⚠️ THEY ASSERT PROPERTIES, NOT A LIST. Nothing here names all eight kinds or
 * pins which fields `report` honours: that is the golden's job, and duplicating
 * it in TypeScript would recreate exactly the second copy the golden exists to
 * abolish. What is asserted is what must be true of ANY valid artefact.
 */
describe("the generated print-template surface", () => {
  it("is not empty, which is what a skipped or broken generator would leave", () => {
    expect(PRINT_TEMPLATE_KINDS.length).toBeGreaterThan(0);
    expect(PRINT_TEMPLATE_FIELDS.length).toBeGreaterThan(0);
    // A generated file with no note is a hand-edited one.
    expect(surface.note).toContain("GENERATED");
  });

  it("gives every document kind at least one field that actually prints", () => {
    for (const kind of PRINT_TEMPLATE_KINDS) {
      expect(
        kind.honours.length,
        `${kind.doc_kind} honours no override field at all, so the editing form ` +
          `for it would be empty and the per-company template silently inert`,
      ).toBeGreaterThan(0);
    }
  });

  it("partitions every field into honours or ignores, with nothing missing or double-counted", () => {
    for (const kind of PRINT_TEMPLATE_KINDS) {
      const seen = [...kind.honours, ...kind.ignores].sort();
      expect(
        seen,
        `${kind.doc_kind} does not account for every field: a field in neither ` +
          `list is one the form cannot reason about, and one in both is a ` +
          `contradiction`,
      ).toEqual([...PRINT_TEMPLATE_FIELDS].sort());
    }
  });

  it("keeps the grid RAGGED, which is the whole fact the frontend consumes", () => {
    // ⛔ IF EVERY KIND HONOURED EVERYTHING, the screen would need no per-kind
    // knowledge and this artefact would be dead weight that still passes. The
    // Go generator asserts this too; it is repeated here because THIS suite is
    // the one that always runs.
    const ragged = PRINT_TEMPLATE_KINDS.some((k) => k.ignores.length > 0);
    expect(
      ragged,
      "every doc kind now honours every override field. Either a renderer " +
        "regressed, or the artefact is stale — do not paper over it by deleting " +
        "this assertion",
    ).toBe(true);
  });

  it("never claims a default for a field the kind does not honour", () => {
    // A default shown for a control that is not on the form is a value nothing
    // can explain; a default shown for a control that IS on the form is the
    // placeholder text an operator reads as "this is what prints today".
    for (const kind of PRINT_TEMPLATE_KINDS) {
      if (!kind.honours.includes("declaration")) {
        expect(
          kind.defaults.declaration,
          `${kind.doc_kind} ignores declaration but carries a default for it`,
        ).toBe("");
      }
      if (!kind.honours.includes("signature_label")) {
        expect(
          kind.defaults.signature_label,
          `${kind.doc_kind} ignores signature_label but carries a default for it`,
        ).toBe("");
      }
    }
  });

  it("flags a dynamic title only where the kind honours a title and has no fixed default", () => {
    // The flag is DERIVED on the server as exactly this condition, so it must
    // agree here. A form that ignores it prints an empty title box that reads as
    // "this company has no title" for the one kind whose default is the
    // document's own name.
    for (const kind of PRINT_TEMPLATE_KINDS) {
      const expected =
        kind.honours.includes("title") && kind.defaults.title === "";
      expect(
        kind.default_title_is_the_documents_own_name,
        `${kind.doc_kind}'s dynamic-title flag disagrees with its own honours/defaults`,
      ).toBe(expected);
    }
  });

  it("has a label for every field, so no control renders nameless", () => {
    for (const field of PRINT_TEMPLATE_FIELDS) {
      expect(PRINT_TEMPLATE_FIELD_LABELS[field] ?? "").not.toBe("");
    }
  });
});

describe("the surface accessors", () => {
  it("resolves a kind the artefact carries and refuses one it does not", () => {
    // Indexed access is `| undefined` under noUncheckedIndexedAccess, and the
    // emptiness of the artefact is asserted in its own case above.
    const first = PRINT_TEMPLATE_KINDS[0];
    if (first === undefined) throw new Error("the artefact carries no kinds");
    expect(printTemplateSurfaceFor(first.doc_kind)?.doc_kind).toBe(
      first.doc_kind,
    );
    // ⛔ `undefined` AND NOT A PERMISSIVE DEFAULT. "invoice" is the plausible
    // wrong answer — it is what everybody calls the document, and the stored
    // vocabulary spells it "tax-invoice". A caller that mistypes must get a
    // visibly broken form, never one offering all seven controls.
    expect(printTemplateSurfaceFor("invoice")).toBeUndefined();
    expect(printTemplateHonours("invoice", "declaration")).toBe(false);
  });

  it("agrees with the artefact about what each kind honours", () => {
    for (const kind of PRINT_TEMPLATE_KINDS) {
      for (const field of PRINT_TEMPLATE_FIELDS) {
        expect(printTemplateHonours(kind.doc_kind, field)).toBe(
          kind.honours.includes(field),
        );
      }
    }
  });

  it("derives a readable kind label by rule rather than from a second table", () => {
    // Derived, so a document kind added on the server appears in the picker with
    // a reasonable name and no frontend change — rather than falling through a
    // lookup to `undefined` and rendering an empty row in a list of eight.
    expect(printTemplateKindLabel("tax-invoice")).toBe("Tax Invoice");
    expect(printTemplateKindLabel("bill-of-supply")).toBe("Bill Of Supply");
    expect(printTemplateKindLabel("report")).toBe("Report");
    // Every kind in the artefact must produce a non-empty label.
    for (const kind of PRINT_TEMPLATE_KINDS) {
      expect(printTemplateKindLabel(kind.doc_kind)).not.toBe("");
    }
  });
});
