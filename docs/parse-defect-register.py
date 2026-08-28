"""Re-derive the 7.1 defect register count by PARSING the rows.

Reads the STATUS COLUMN only (never the whole line): CLOSED rows say "OPEN" in
their prose and a line-wide test has been wrong in both directions.
"""
import re, sys
src = open(sys.argv[1] if len(sys.argv) > 1 else 'HANDOFF-PARITY-AUDIT.md').read().split('\n')
start = next(i for i, l in enumerate(src) if l.startswith('| # | Row | Sev | Defect | Where | Status |'))
end = start + 1
while end < len(src) and src[end].startswith('|'):
    end += 1
rows = [l for l in src[start + 2:end] if re.match(r'^\|\s*\*{0,2}D\d+\*{0,2}\s*\|', l)]
b = {'OPEN': [], 'CLOSED': [], 'PARTIAL': [], '?': []}
for l in rows:
    cells = [c.strip() for c in l.strip('|').split('|')]
    ident, status = re.sub(r'[*\s]', '', cells[0]), cells[-1]
    closed = re.search(r'\b(CLOSED|FIXED|DONE)\b', status) is not None
    partial = re.search(r'\bPARTIAL\b', status) is not None
    op = re.search(r'\bOPEN\b', status) is not None
    key = ('PARTIAL' if partial and not closed else
           'CLOSED' if closed and not op else
           'OPEN' if op and not closed else '?')
    b[key].append(ident)
print(f"{len(rows)} REGISTERED · {len(b['CLOSED'])} CLOSED · {len(b['OPEN'])} OPEN · "
      f"{len(b['PARTIAL'])} PARTIAL · {len(b['?'])} UNREADABLE")
for k in ('OPEN', 'PARTIAL', '?'):
    if b[k]:
        print(f"  {k}: {', '.join(b[k])}")
