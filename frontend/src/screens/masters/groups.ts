/**
 * The canonical TallyPrime group tree (RESEARCH.md sec 5): 15 primary groups
 * (9 Balance-Sheet, 6 Profit & Loss) + 13 sub-groups = 28 predefined groups.
 *
 * Master-create screens offer these as the `Under` selection-list options for a
 * new ledger, and (minus a couple of Primary-only choices) as parent options
 * for a new group, mirroring the muscle-memory order a Tally user expects.
 */

/** The 9 Balance-Sheet primary groups. */
export const BALANCE_SHEET_PRIMARY_GROUPS = [
  "Capital Account",
  "Loans (Liability)",
  "Current Liabilities",
  "Fixed Assets",
  "Investments",
  "Current Assets",
  "Branch / Divisions",
  "Misc. Expenses (ASSET)",
  "Suspense A/c",
] as const;

/** The 6 Profit & Loss primary groups. */
export const PROFIT_LOSS_PRIMARY_GROUPS = [
  "Sales Accounts",
  "Purchase Accounts",
  "Direct Incomes",
  "Indirect Incomes",
  "Direct Expenses",
  "Indirect Expenses",
] as const;

/** The 13 predefined sub-groups. */
export const SUB_GROUPS = [
  "Bank Accounts",
  "Cash-in-Hand",
  "Deposits (Asset)",
  "Loans & Advances (Asset)",
  "Stock-in-Hand",
  "Sundry Debtors",
  "Duties & Taxes",
  "Provisions",
  "Sundry Creditors",
  "Bank OD A/c",
  "Secured Loans",
  "Unsecured Loans",
  "Reserves & Surplus",
] as const;

/**
 * All 28 predefined groups available as a ledger's `Under` (parent). Ordered
 * sub-groups first (the common case for a working ledger), then primaries.
 */
export const LEDGER_PARENT_GROUPS: readonly string[] = [
  ...SUB_GROUPS,
  ...BALANCE_SHEET_PRIMARY_GROUPS,
  ...PROFIT_LOSS_PRIMARY_GROUPS,
];

/**
 * Parent options for a NEW group. "Primary" lets the user create a top-level
 * user group; the 28 predefined groups follow as nestable parents.
 */
export const GROUP_PARENT_OPTIONS: readonly string[] = [
  "Primary",
  ...LEDGER_PARENT_GROUPS,
];

/** Nature-of-group choices shown when a group is created Under "Primary". */
export const GROUP_NATURES = [
  "Assets",
  "Liabilities",
  "Income",
  "Expenses",
] as const;
export type GroupNature = (typeof GROUP_NATURES)[number];
