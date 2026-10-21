#!/usr/bin/env python3
"""Apply an explicitly authorized cap increase without resetting shared spend.

This command does not obtain authorization. The operator must first obtain the
user's approval for the concrete increase, then supply that approval verbatim.
The exact pre-update bytes are backed up once and hashed in an appended audit.
"""
import argparse
from datetime import datetime, timezone
from decimal import Decimal
import fcntl
import hashlib
import json
import os
from pathlib import Path
import tempfile


def increase(path, *, expected_cap, expected_charge, new_cap, authorization):
    path = Path(path)
    old, charged, new = map(Decimal, (expected_cap, expected_charge, new_cap))
    if not all(n.is_finite() for n in (old, charged, new)) or old <= 0 or charged < 0 or charged > old or new <= old:
        raise ValueError('invalid cap increase')
    if not authorization.strip():
        raise ValueError('explicit authorization text is required')
    with open(str(path) + '.lock', 'a') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        raw = path.read_bytes()  # Missing ledger is an error, never a fresh budget.
        data = json.loads(raw)
        if Decimal(data['cap_usd']) != old or Decimal(data['charged_usd']) != charged:
            raise ValueError('ledger changed; refusing to reset cap or spend')
        digest = hashlib.sha256(raw).hexdigest()
        backup = path.with_name(path.name + '.before-increase-' + digest + '.json')
        # An interrupted attempt can reuse only its byte-identical backup.
        try:
            fd = os.open(backup, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        except FileExistsError:
            if backup.read_bytes() != raw:
                raise ValueError('backup conflict')
        else:
            with os.fdopen(fd, 'wb') as out:
                out.write(raw)
                out.flush()
                os.fsync(out.fileno())
        entry = {
            'at': datetime.now(timezone.utc).isoformat(),
            'old_cap_usd': str(old), 'new_cap_usd': str(new),
            'additional_usd': str(new - old), 'charged_usd': str(charged),
            'authorization': authorization,
            'previous_sha256': digest, 'backup_path': str(backup),
        }
        data.setdefault('cap_increases', []).append(entry)
        data['cap_usd'] = str(new)
        name = None
        try:
            with tempfile.NamedTemporaryFile(mode='w', dir=path.parent, prefix='.budget-increase-', delete=False) as out:
                name = out.name
                json.dump(data, out, indent=2)
                out.write('\n')
                out.flush()
                os.fsync(out.fileno())
            os.replace(name, path)
            name = None
            directory = os.open(path.parent, os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if name:
                os.unlink(name)
        return entry


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ledger', type=Path, required=True)
    parser.add_argument('--expected-cap', required=True)
    parser.add_argument('--expected-charge', required=True)
    parser.add_argument('--new-cap', required=True)
    parser.add_argument('--authorization', required=True)
    args = parser.parse_args()
    print(json.dumps(increase(args.ledger, expected_cap=args.expected_cap,
                             expected_charge=args.expected_charge, new_cap=args.new_cap,
                             authorization=args.authorization), indent=2))
