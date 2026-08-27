import { describe, expect, it } from "vitest";

import { ApiError, NetworkError } from "../api/http";

import { classifyBatchRefusal, refusalRemedy } from "./commitRefusal";

describe("⛔ a whole-batch refusal is not a per-voucher status", () => {
  it("a cutoff 403 is a CUTOFF, and the server's sentence (which names the date) survives verbatim", () => {
    const msg = "voucher date is before the back-dated cutoff of 2026-04-01";
    const r = classifyBatchRefusal(new ApiError(403, msg));
    expect(r.kind).toBe("cutoff");
    expect(r.message, "the date is the one fact the operator needs").toBe(msg);
  });

  it("⛔ a STEPUP 403 is NOT read as a cutoff and NOT as a denial", () => {
    const r = classifyBatchRefusal(
      new ApiError(403, "webauthn step-up required", { code: "STEPUP_REQUIRED" }),
    );
    expect(r.kind).toBe("stepup");
  });

  it("⛔ a cutoff wins over the step-up test — both are 403 and only the sentence separates them", () => {
    // A server that sent both signals must still be read as the cutoff: the
    // remedy is a date, and telling the operator to re-tap a passkey sends them
    // to fix something they already did.
    const r = classifyBatchRefusal(
      new ApiError(403, "this voucher is dated 2026-03-31, inside the period closed by the back-dated cutoff of 2026-04-01", {
        code: "STEPUP_REQUIRED",
      }),
    );
    expect(r.kind).toBe("cutoff");
  });

  it("a plain ACL 403 is neither — it is `other`, shown verbatim", () => {
    const r = classifyBatchRefusal(new ApiError(403, "level may not create sales"));
    expect(r.kind).toBe("other");
    expect(r.message).toBe("level may not create sales");
  });

  it("no answer at all is `offline`, and says nothing was sent", () => {
    const r = classifyBatchRefusal(new NetworkError("POST /sync/commit", new TypeError("Failed to fetch")));
    expect(r.kind).toBe("offline");
    expect(r.message).toMatch(/still queued/i);
  });

  it("the mock's honest 501 is `other` and its sentence is preserved for the screen", () => {
    const r = classifyBatchRefusal(new ApiError(501, "the mock has no cloud sequencer: committing an outbox needs the real numbering allocator"));
    expect(r.kind).toBe("other");
    expect(r.message).toMatch(/no cloud sequencer/);
  });

  it("⛔ THE PROPERTY: every kind has a remedy, and NO remedy ever tells the operator their vouchers failed or to re-key them", () => {
    const kinds = ["cutoff", "stepup", "offline", "other"] as const;
    for (const kind of kinds) {
      const remedy = refusalRemedy({ kind, status: 403, message: "m" });
      expect(remedy.length, kind).toBeGreaterThan(30);
      // The dangerous direction, asserted as a property over all four rather
      // than as a list: nothing here may suggest the work is gone.
      expect(remedy, kind).not.toMatch(/re-?key|failed|rejected|lost|discarded your/i);
    }
  });
});
