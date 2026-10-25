"""Maintain human review records; invoke only after reading the specified code."""
import argparse
import csv
from pathlib import Path

root = Path(__file__).resolve().parents[4]
ledger = Path(__file__).resolve().parents[1] / 'file-review-ledger.csv'
parser = argparse.ArgumentParser()
parser.add_argument('status', choices=['static_reviewed','partial','contract_checked','material_checked','excluded','pending'])
parser.add_argument('note')
parser.add_argument('paths', nargs='+')
parser.add_argument('--ranges', default='all')
args = parser.parse_args()
with ledger.open() as stream:
    reader = csv.DictReader(stream)
    fields = reader.fieldnames
    rows = list(reader)
known = {r['path'] for r in rows}
assert set(args.paths) <= known, set(args.paths) - known
for row in rows:
    if row['path'] not in args.paths:
        continue
    assert (root / row['path']).is_file()
    row['status'] = args.status
    row['reviewed_ranges'] = f"1-{row['lines']}" if args.ranges == 'all' and args.status != 'pending' else args.ranges
    row['notes'] = args.note
with ledger.open('w') as stream:
    writer = csv.DictWriter(stream, fieldnames=fields)
    writer.writeheader()
    writer.writerows(rows)
print(f'{args.status}: {len(args.paths)} files')
