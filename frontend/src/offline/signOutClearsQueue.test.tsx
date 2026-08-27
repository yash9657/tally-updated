import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { AUTH_STORAGE_KEY, AuthProvider, useAuth } from "../auth";

import { clearOutboxStores, memoryOutboxStore } from "./store";
import { OfflineNode } from "./outbox";

/**
 * signOutClearsQueue.test.tsx — ⛔ THE SECURITY PROPERTY THAT WAS A COMMENT.
 *
 * `store.ts`'s header has said since the day it was written that
 * `clearOutboxStores` is "called on SIGN-OUT", mirroring the rule
 * `CompanyContext.tsx:60` documents for the persisted company id. MEASURED when
 * the queue was first wired up:
 *
 *     /usr/bin/grep -rn "clearOutboxStores" src/ | grep -v ^src/offline/   -> EMPTY
 *
 * Nothing called it. The header was a promise, not a behaviour — and what it
 * promises matters more than the company id it mirrors, because what is left
 * behind is UNENCRYPTED BOOKS: un-posted vouchers keyed by one operator, sitting
 * in a shared back-office browser under a subject nobody is signed in as, which
 * the next person to sign in could commit under their own name.
 *
 * ⛔ AND IT CANNOT BE TESTED BY READING `signOut`. The call is fire-and-forget
 * (sign-out is synchronous — it is invoked from a click AND from a 401 handler —
 * and awaiting IndexedDB there would either make it async everywhere or strand a
 * signed-out operator behind a storage error), so the only honest oracle is the
 * STORE'S CONTENTS afterwards.
 */

const CO = "co-1";
const SUBJ = "user:alice";

const SESSION = {
  token: "test-token",
  user: { subject: SUBJ, display_name: "Alice" },
};

beforeEach(async () => {
  await clearOutboxStores();
  globalThis.sessionStorage?.clear();
  globalThis.sessionStorage?.setItem(AUTH_STORAGE_KEY, JSON.stringify(SESSION));
});

/** A queue with one un-committed voucher in it, written the way the app writes. */
async function seedQueue(): Promise<void> {
  const store = memoryOutboxStore(CO, SUBJ);
  const node = new OfflineNode({ nodeID: "node-a", fy: "2025-26" });
  node.createVoucher(1756300000000, "Journal", { narration: "not posted yet" });
  await store.save({ ...node.snapshot(), nodeID: "node-a" });
}

function SignOutButton() {
  const auth = useAuth();
  return (
    <button type="button" onClick={() => auth?.signOut()}>
      Sign Out
    </button>
  );
}

describe("⛔ signing out destroys the offline queue", () => {
  it("an un-committed voucher does NOT survive the sign-out that follows it", async () => {
    await seedQueue();
    // The control: it really is there before the sign-out, so the assertion
    // below is about the sign-out and not about an empty store.
    expect((await memoryOutboxStore(CO, SUBJ).load())?.outbox).toHaveLength(1);

    render(
      <AuthProvider baseUrl="" fetchImpl={() => Promise.reject(new Error("no auth calls expected"))}>
        <SignOutButton />
      </AuthProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));

    await waitFor(async () => {
      expect(await memoryOutboxStore(CO, SUBJ).load()).toBeNull();
    });
  });

  it("⛔ ANOTHER operator's queue on the same browser goes too", async () => {
    // Deliberate, and stated in `clearOutboxStores`: signing out is the moment
    // the machine stops being this operator's. A queue keyed to a subject nobody
    // is signed in as is exactly the residue this rule exists to remove.
    const other = memoryOutboxStore(CO, "user:bob");
    const node = new OfflineNode({ nodeID: "node-b", fy: "2025-26" });
    node.createVoucher(1756300000000, "Journal", { narration: "bob's" });
    await other.save({ ...node.snapshot(), nodeID: "node-b" });

    render(
      <AuthProvider baseUrl="" fetchImpl={() => Promise.reject(new Error("no auth calls expected"))}>
        <SignOutButton />
      </AuthProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));

    await waitFor(async () => {
      expect(await other.load()).toBeNull();
    });
  });
});
