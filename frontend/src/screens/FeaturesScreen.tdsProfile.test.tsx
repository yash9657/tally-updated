import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
} from "@testing-library/react";

import { FeaturesScreen } from "./FeaturesScreen";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient, ApiError } from "../api";
import type { CoredClient } from "../api";
import type {
  DeductorCategory,
  SetTDSProfileRequest,
  TDSProfileResponse,
} from "../api/types";

/**
 * F11 > TDS Return Deductor Profile — the deductor block of the TDS return
 * (migration 0070), rendered as a panel on F11 because Tally asks for exactly
 * this behind F11 > Enable TDS > "TDS Deductor Details".
 *
 * WHAT THESE TESTS CANNOT PROVE, said plainly:
 *
 *  • Nothing here proves a saved profile makes a return BUILDABLE. The mock
 *    knows nothing about Form 140 and the reporting layer is not in this
 *    process; the register's return-file status is what answers that, and
 *    form140's own tests are what pin it.
 *  • The refusal SENTENCES in the mock are a hand copy of the server's. So no
 *    test here retypes one: the 400 case captures the rejected ApiError and
 *    looks for THAT message in the DOM, which pins "the screen renders whatever
 *    the server said" rather than pinning the mock's current wording.
 *  • The mock has no ACL and cannot answer 403, so the Data-Entry path is driven
 *    with an injected client that throws one.
 *  • NOTHING ASSERTS A HARD-CODED CATEGORY LIST. The sixteen Annexure 4 letters
 *    and their two conditional rules are SERVED, and a TS copy of them is the
 *    drift bug this feature has already had; so every test below reads the
 *    vocabulary out of the response and asserts the form follows it — including
 *    one test that serves an INVENTED category to prove the picker cannot be
 *    coming from a constant in the client.
 */

afterEach(cleanup);

const COMPANY = { id: "company-demo", name: "Demo Traders" };

function renderFeatures(client: CoredClient) {
  return render(
    <CompanyProvider client={client} initialCompany={COMPANY}>
      <FeaturesScreen />
    </CompanyProvider>,
  );
}

/** The vocabulary as the SERVER serves it — never a literal in this file. */
async function servedCategories(): Promise<DeductorCategory[]> {
  const res = await createMockClient().getTDSProfile(COMPANY.id);
  return res.deductor_categories;
}

function option(picker: HTMLElement, code: string): void {
  fireEvent.change(picker, { target: { value: code } });
}

function type(label: string, value: string): void {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

function valueOf(label: string): string {
  return (screen.getByLabelText(label) as HTMLInputElement).value;
}

/**
 * Every mandatory field except the category, which each test picks for itself
 * from the served list. The TAN is keyed in LOWER CASE on purpose: the server
 * normalises it and the read-back is what the operator must be shown.
 */
function fillMandatoryFields(): void {
  type("TAN", "blrc12345e");
  type("TAN name (as the department holds it)", "Demo Traders Private Limited");
  type("Deductor PAN", "AAACB1234C");
  type("Deductor address line 1", "12 MG Road");
  type("Deductor state code", "29");
  type("Deductor pincode", "560001");
  type("Deductor e-mail", "tds@demo.example");
  type("Deductor telephone", "8012345678");
  type("Responsible person name", "Anita Rao");
  type("Responsible person designation", "Finance Manager");
  type("Responsible person PAN", "AAAPA1234B");
  type("Responsible person address line 1", "12 MG Road");
  type("Responsible person state code", "29");
  type("Responsible person pincode", "560001");
  type("Responsible person telephone", "8012345679");
}

describe("F11 TDS Return Deductor Profile — the served vocabulary", () => {
  it("says NO RETURN CAN BE BUILT while nothing is recorded", async () => {
    renderFeatures(createMockClient());
    // The absence of the row is the refusal, and it is the FIRST thing the
    // return checks — before a challan or a deduction. A panel that merely
    // showed an empty form would leave the operator to discover that at filing
    // time.
    expect(
      await screen.findByText(/No deductor profile on record/),
    ).toBeTruthy();
    expect(screen.getByText(/NO TDS RETURN CAN BE BUILT/)).toBeTruthy();
  });

  it("populates the category picker from the RESPONSE, not from a constant", async () => {
    // ⚠️ THE ASSERTION THIS FEATURE EXISTS FOR. A served list asserted against
    // the mock's own list would pass just as well if the client held a copy, so
    // this response serves a category that is NOT in Annexure 4 at all: if the
    // picker is drawn from anything but the response, "Z" cannot appear and the
    // sixteen real letters will.
    const invented: DeductorCategory[] = [
      {
        code: "Z",
        description: "Invented category served only by this test",
        state_name_code_required: false,
        ministry_required: false,
        ministry_allowed: false,
      },
      {
        code: "Y",
        description: "Second invented category",
        state_name_code_required: true,
        ministry_required: false,
        ministry_allowed: false,
      },
    ];
    const res: TDSProfileResponse = {
      profile: null,
      profile_recorded: false,
      deductor_categories: invented,
      pan_not_required_literal: "NOPANFORTHISTEST",
    };
    const client = {
      ...createMockClient(),
      getTDSProfile: async () => res,
    } as unknown as CoredClient;
    renderFeatures(client);

    const picker = await screen.findByLabelText("Annexure 4 deductor category");
    const codes = Array.from(picker.querySelectorAll("option"))
      .map((o) => (o as HTMLOptionElement).value)
      .filter((v) => v !== "");
    expect(codes).toEqual(["Z", "Y"]);
    expect(screen.getByText(/Invented category served only by this test/))
      .toBeTruthy();

    // The PAN-not-required literal is served for the same reason and must not be
    // spelled by the client either.
    expect(
      screen.getByRole("button", { name: "Use NOPANFORTHISTEST" }),
    ).toBeTruthy();
  });

  it("does NOT pre-select a category, and refuses to submit without one", async () => {
    renderFeatures(createMockClient());
    const picker = (await screen.findByLabelText(
      "Annexure 4 deductor category",
    )) as HTMLSelectElement;
    // A letter that goes on a statutory return must be chosen. Defaulting it
    // would file a claim about who this deductor is that nobody made.
    expect(picker.value).toBe("");
    expect(
      (screen.getByRole("button", { name: "Record the profile" }) as
        HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it("shows the sixteen served letters WITH their descriptions", async () => {
    const served = await servedCategories();
    renderFeatures(createMockClient());
    const picker = await screen.findByLabelText("Annexure 4 deductor category");
    const codes = Array.from(picker.querySelectorAll("option"))
      .map((o) => (o as HTMLOptionElement).value)
      .filter((v) => v !== "");
    expect(codes).toEqual(served.map((c) => c.code));
    // The letter alone is unreadable — "K" tells an operator nothing — so the
    // description the server sends travels with it.
    for (const c of served) {
      expect(
        screen.getByRole("option", { name: `${c.code} — ${c.description}` }),
      ).toBeTruthy();
    }
  });
});

describe("F11 TDS Return Deductor Profile — the round trip", () => {
  it("saves, shows the SERVER's normalisation, and states the overwrite consequence", async () => {
    const served = await servedCategories();
    // A category with no conditional field at all, chosen FROM THE RULES rather
    // than by letter.
    const plain = served.find(
      (c) =>
        !c.state_name_code_required && !c.ministry_required && !c.ministry_allowed,
    );
    if (!plain) throw new Error("no unconditional category was served");

    renderFeatures(createMockClient());
    await screen.findByLabelText("Annexure 4 deductor category");
    fillMandatoryFields();
    option(screen.getByLabelText("Annexure 4 deductor category"), plain.code);
    fireEvent.click(screen.getByRole("button", { name: "Record the profile" }));

    // The form is re-seeded from the READ-BACK, so the operator sees the value
    // the return will carry rather than the one they keyed: a lower-case TAN
    // comes back in capitals.
    await waitFor(() => expect(valueOf("TAN")).toBe("BLRC12345E"));
    expect(screen.getByText("BLRC12345E")).toBeTruthy();
    expect(
      screen.getByText(new RegExp(`category ${plain.code} \\(`)),
    ).toBeTruthy();

    // ⚠️ THE CONSEQUENCE MIGRATION 0070 WRITES DOWN, on screen. This table keeps
    // no history, so re-emitting an old quarter names whoever is recorded NOW.
    expect(
      screen.getByText(/produces a file naming Anita Rao/),
    ).toBeTruthy();
    expect(screen.getByText(/this table keeps no history/)).toBeTruthy();

    // ...and the button says what a second save does, because the panel
    // directly above it APPENDS a row per year and these two forms look alike.
    expect(
      screen.getByRole("button", { name: "Overwrite the profile" }),
    ).toBeTruthy();
  });

  it("states that the TAN is not the GSTIN, before the mistake is made", async () => {
    renderFeatures(createMockClient());
    await screen.findByLabelText("TAN");
    // The single most likely operator error on this form. The server refuses it
    // with this fact, but by then the operator believes they have keyed their
    // tax number — so the form says it where the field is.
    expect(screen.getByText(/THE TAN IS NOT THE GSTIN/)).toBeTruthy();
    expect(
      screen.getByText(/cannot be derived from it/),
    ).toBeTruthy();
  });
});

describe("F11 TDS Return Deductor Profile — the conditional fields", () => {
  it("shows the Annexure 5 state code only for the categories that REQUIRE it", async () => {
    const served = await servedCategories();
    const needs = served.find((c) => c.state_name_code_required);
    const forbids = served.find((c) => !c.state_name_code_required);
    if (!needs || !forbids) throw new Error("served rules cover only one case");

    renderFeatures(createMockClient());
    const picker = await screen.findByLabelText(
      "Annexure 4 deductor category",
    );

    // Hidden for a category the format FORBIDS it for — a shown-but-forbidden
    // field is a 400 exactly as a hidden-but-required one is.
    option(picker, forbids.code);
    expect(screen.queryByLabelText("Annexure 5 state name code")).toBeNull();

    option(picker, needs.code);
    expect(screen.getByLabelText("Annexure 5 state name code")).toBeTruthy();
  });

  it("shows the Annexure 3 ministry only where the rules allow it, and field 58 only for code 99", async () => {
    const served = await servedCategories();
    const needsMinistry = served.find((c) => c.ministry_required);
    const noMinistry = served.find(
      (c) => !c.ministry_required && !c.ministry_allowed,
    );
    if (!needsMinistry || !noMinistry) {
      throw new Error("served rules cover only one ministry case");
    }

    renderFeatures(createMockClient());
    const picker = await screen.findByLabelText(
      "Annexure 4 deductor category",
    );

    option(picker, noMinistry.code);
    expect(screen.queryByLabelText("Annexure 3 ministry code")).toBeNull();

    option(picker, needsMinistry.code);
    expect(screen.getByLabelText("Annexure 3 ministry code")).toBeTruthy();

    // Field 58 is an EQUIVALENCE with field 57, not an optional extra: the code
    // is 99 exactly when the ministry is named. So the field appears on 99 and
    // on nothing else.
    expect(screen.queryByLabelText("Ministry name (code 99, Other)")).toBeNull();
    type("Annexure 3 ministry code", "99");
    expect(
      screen.getByLabelText("Ministry name (code 99, Other)"),
    ).toBeTruthy();
    type("Annexure 3 ministry code", "01");
    await waitFor(() =>
      expect(
        screen.queryByLabelText("Ministry name (code 99, Other)"),
      ).toBeNull(),
    );
  });

  it("CLEARS a conditional value the new category forbids, instead of posting it", async () => {
    // ⚠️ THE CASE THAT WOULD OTHERWISE BE A 400 THE OPERATOR CANNOT SEE THE
    // CAUSE OF: they pick a State-Government category, key the state code, then
    // realise they are a Company and change the letter. The keyed value is now
    // FORBIDDEN, the field that held it is gone, and a PATCH-shaped save would
    // send it anyway. The whole row is PUT and what is not shown is cleared.
    const served = await servedCategories();
    const needs = served.find((c) => c.state_name_code_required);
    const plain = served.find(
      (c) =>
        !c.state_name_code_required && !c.ministry_required && !c.ministry_allowed,
    );
    if (!needs || !plain) throw new Error("served rules cover only one case");

    let sent: SetTDSProfileRequest | null = null;
    const base = createMockClient();
    const client = {
      ...base,
      setTDSProfile: async (id: string, req: SetTDSProfileRequest) => {
        sent = req;
        return await base.setTDSProfile(id, req);
      },
    } as unknown as CoredClient;

    renderFeatures(client);
    const picker = await screen.findByLabelText(
      "Annexure 4 deductor category",
    );
    fillMandatoryFields();
    option(picker, needs.code);
    type("Annexure 5 state name code", "29");
    option(picker, plain.code);
    fireEvent.click(screen.getByRole("button", { name: "Record the profile" }));

    // It saved, which the server would have refused had the value been sent...
    await waitFor(() => expect(valueOf("TAN")).toBe("BLRC12345E"));
    // ...and the payload proves the clearing rather than inferring it.
    const req = sent as SetTDSProfileRequest | null;
    if (req === null) throw new Error("the profile was never PUT");
    expect(req.state_name_code).toBe("");
    expect(req.deductor_category).toBe(plain.code);
  });
});

describe("F11 TDS Return Deductor Profile — the refusals", () => {
  it("renders the SERVER's sentence on a 400, whatever that sentence says", async () => {
    // The sentence is captured off the rejected ApiError rather than retyped
    // here: a literal in a test pins the MOCK's wording, and every sentence in
    // the mock is a hand copy of the server's. What must hold is that whatever
    // the server said reaches the operator's eyes intact — it names the field,
    // its BH number and what to supply instead.
    let refused = "";
    const base = createMockClient();
    const client = {
      ...base,
      setTDSProfile: async (id: string, req: SetTDSProfileRequest) => {
        try {
          return await base.setTDSProfile(id, req);
        } catch (e) {
          refused = e instanceof Error ? e.message : String(e);
          throw e;
        }
      },
    } as unknown as CoredClient;

    const served = await servedCategories();
    const plain = served.find(
      (c) =>
        !c.state_name_code_required && !c.ministry_required && !c.ministry_allowed,
    );
    if (!plain) throw new Error("no unconditional category was served");

    renderFeatures(client);
    await screen.findByLabelText("TAN");
    fillMandatoryFields();
    option(screen.getByLabelText("Annexure 4 deductor category"), plain.code);
    // The error this form exists to catch: the company's GSTIN keyed as the TAN.
    type("TAN", "29AAACB1234C1ZK");
    fireEvent.click(screen.getByRole("button", { name: "Record the profile" }));

    await waitFor(() => expect(refused).not.toBe(""));
    await waitFor(() => expect(screen.getByText(refused)).toBeTruthy());
    // Nothing was written, so the panel still says no return can be built.
    expect(screen.getByText(/No deductor profile on record/)).toBeTruthy();
  });

  it("renders a 403 as the DECISION it is, with the server's sentence", async () => {
    // The mock has no ACL and cannot produce this, so it is injected. Data Entry
    // is refused this route BY DESIGN — the row carries a named individual's
    // PAN — and a panel that showed a blank form or an error badge would send
    // someone hunting a bug that is a policy.
    const client = {
      ...createMockClient(),
      getTDSProfile: async () => {
        throw new ApiError(403, "default access: none");
      },
    } as unknown as CoredClient;
    renderFeatures(client);

    await waitFor(() =>
      expect(
        screen.getByText(
          /You do not have permission to read or record the deductor profile: default access: none/,
        ),
      ).toBeTruthy(),
    );
    expect(screen.getByText(/intended answer rather than a fault/)).toBeTruthy();
    expect(screen.getByText(/named individual/)).toBeTruthy();
    expect(screen.getByText("allow display tds-profile")).toBeTruthy();
    // And the form is GONE rather than shown and doomed to a 403 on save.
    expect(screen.queryByLabelText("TAN")).toBeNull();
    // The panel above it is unaffected: they are separate routes and separate
    // decisions, so one refusal must not blank the other panel.
    expect(screen.getByLabelText("Deductor type")).toBeTruthy();
  });
});

describe("F11 — the two deductor concepts stay separate", () => {
  it("keeps the s.402(11) deductor type and the Annexure 4 category apart", async () => {
    // ⛔ DIFFERENT TABLES, DIFFERENT VOCABULARIES, AND THE MAPPING IS A DECISION
    // RATHER THAN A DERIVATION (migration 0070). Two pickers, two panels, and
    // choosing in one must not fill the other.
    renderFeatures(createMockClient());
    const annexure = await screen.findByLabelText(
      "Annexure 4 deductor category",
    );
    const entity = screen.getByLabelText("Deductor type");
    expect(annexure).not.toBe(entity);

    const served = await servedCategories();
    const first = served[0];
    if (!first) throw new Error("no categories served");
    option(annexure, first.code);
    expect((entity as HTMLSelectElement).value).toBe("");

    option(entity, "COMPANY");
    expect((annexure as HTMLSelectElement).value).toBe(first.code);

    // The entity categories are words and the Annexure 4 codes are letters, so
    // neither picker can be offering the other's vocabulary.
    const entityValues = Array.from(entity.querySelectorAll("option"))
      .map((o) => (o as HTMLOptionElement).value)
      .filter((v) => v !== "");
    expect(entityValues).not.toContain(first.code);

    // And the heading says which question each answers.
    expect(
      screen.getByText(/TDS Return Deductor Profile — who is FILING/),
    ).toBeTruthy();
    expect(
      screen.getByText(/TDS Deductor Details \(s.402\(11\)/),
    ).toBeTruthy();
  });
});
