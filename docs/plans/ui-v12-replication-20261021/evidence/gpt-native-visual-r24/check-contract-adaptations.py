"""Check tracking guard rejects both baseline removal and unaudited additions."""
import copy
import importlib.util
from pathlib import Path
from unittest.mock import patch

path = Path(__file__).resolve().parents[2] / 'validate-progress.py'
spec = importlib.util.spec_from_file_location('progress_validator', path)
validator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validator)
original_rows = validator.rows
assert validator.validate()['slices'] == 458
for change in ('remove_baseline', 'add_unrecorded'):
    def changed_rows(name):
        rows = copy.deepcopy(original_rows(name))
        if name == 'acceptance.csv':
            account = next(row for row in rows if row['slice_id'] == 'SL-003')
            candidates = account['contract_candidates'].split(';')
            if change == 'remove_baseline':
                candidates.remove('C-001')
            else:
                candidates.append('C-002')
            account['contract_candidates'] = ';'.join(candidates)
        return rows
    with patch.object(validator, 'rows', changed_rows):
        try:
            validator.validate()
        except AssertionError as error:
            assert 'unrecorded contract adaptation' in str(error), error
        else:
            raise AssertionError(f'{change} was accepted')
print('458-state baseline passed; removed baseline and unaudited addition rejected')
