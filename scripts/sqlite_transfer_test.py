import importlib.util
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
TOOL = ROOT / 'scripts/sqlite-transfer.py'
spec = importlib.util.spec_from_file_location('transfer', TOOL)
transfer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(transfer)


class TransferTest(unittest.TestCase):
    def test_live_wal_backup_round_trip_preserves_private_links_and_rows(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            source = base / 'live.db'
            with sqlite3.connect(source) as db:
                db.execute('PRAGMA journal_mode=WAL')
                db.execute('PRAGMA wal_autocheckpoint=0')
                for migration in sorted((ROOT / 'assets/migrations').glob('*.sql')):
                    db.executescript(migration.read_text().split('-- +goose Down')[0])
                db.execute('INSERT INTO recipients VALUES(7,?,?,?,?,?)', ("O'Connor", 'one, two', '21:15', 'Australia/Brisbane', 1))
                db.execute('INSERT INTO notification_targets VALUES(19,7,?,?,1)', ('email', '{"address":"test@example.invalid"}'))
                db.execute('INSERT INTO digest_snapshots VALUES(42,7,?,?,?,?,?)', ('2026-11-04', 'unchanged-private-token', json.dumps([{'summary': "<A & B>\n你好"}]), '2026-11-03T11:15:00Z', None))
                db.commit()
                self.assertTrue(Path(str(source) + '-wal').stat().st_size > 0)
                backup = base / 'backup.db'
                subprocess.run([sys.executable, TOOL, 'backup', source, backup], check=True, capture_output=True)
                exported = base / 'import.sql'
                subprocess.run([sys.executable, TOOL, 'export', backup, exported], check=True, capture_output=True)
                with sqlite3.connect(base / 'd1.db') as destination:
                    destination.executescript((ROOT / 'worker/migrations/0001_worker.sql').read_text())
                    destination.executescript(exported.read_text())
                    self.assertEqual(transfer.rows_and_manifest(destination)[1], transfer.rows_and_manifest(db)[1])
                    self.assertEqual(destination.execute('SELECT token,generation FROM digest_snapshots').fetchone(), ('unchanged-private-token', 1))
                    with self.assertRaises(sqlite3.IntegrityError):
                        destination.executescript(exported.read_text())

    def test_export_refuses_unfinished_sends_in_both_directions(self):
        for worker in [False, True]:
            with self.subTest(worker=worker), tempfile.TemporaryDirectory() as directory:
                source = Path(directory) / 'db.sqlite'
                with sqlite3.connect(source) as db:
                    db.executescript((ROOT / 'worker/migrations/0001_worker.sql').read_text())
                    if worker:
                        db.execute('INSERT INTO recipients VALUES(1,?,?,?,?,1)', ('Test', 'calendar', '21:00', 'Australia/Brisbane'))
                        db.execute('INSERT INTO notification_targets VALUES(1,1,?,?,1)', ('sms', '{"phone":"123"}'))
                        db.execute("INSERT INTO outbox(dedupe_key,target_id,payload,available_at) VALUES('pending',1,'{}',0)")
                    else:
                        db.execute('CREATE TABLE river_job(kind TEXT,state TEXT)')
                        db.execute("INSERT INTO river_job VALUES('send','retryable')")
                    db.commit()
                output = Path(directory) / 'import.sql'
                result = subprocess.run([sys.executable, TOOL, 'export', source, output], capture_output=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(b'unfinished', result.stderr)
                self.assertFalse(output.exists())
