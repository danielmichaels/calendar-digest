#!/usr/bin/env python3
"""Make a consistent SQLite backup, or export application rows for an empty D1/Go schema."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import sys

TABLES = {
    'recipients': ['id', 'name', 'calendar_id', 'notify_time', 'tz', 'enabled'],
    'notification_targets': ['id', 'recipient_id', 'kind', 'config', 'enabled'],
    'digest_snapshots': ['id', 'recipient_id', 'digest_date', 'token', 'events', 'created_at', 'notified_at'],
    'app_state': ['key', 'value', 'updated_at'],
}


def connect(path):
    return sqlite3.connect(Path(path).resolve().as_uri() + '?mode=ro', uri=True)


def check(db):
    if db.execute('PRAGMA integrity_check').fetchall() != [('ok',)]:
        raise ValueError('SQLite integrity check failed')
    if db.execute('PRAGMA foreign_key_check').fetchall():
        raise ValueError('SQLite foreign key check failed')


def rows_and_manifest(db):
    check(db)
    tables = {}
    manifest = {}
    for table, columns in TABLES.items():
        rows = db.execute(f"SELECT {','.join(columns)} FROM {table} ORDER BY {columns[0]}").fetchall()
        if table == 'digest_snapshots':
            for row in rows:
                if not isinstance(json.loads(row[4]), (list, type(None))):
                    raise ValueError('Snapshot events must be a JSON array or null')
        tables[table] = rows
        data = json.dumps(rows, ensure_ascii=False, separators=(',', ':')).encode()
        manifest[table] = {'rows': len(rows), 'sha256': hashlib.sha256(data).hexdigest()}
    return tables, manifest


def pending(db):
    names = {r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
    if 'river_job' in names:
        count = db.execute("SELECT COUNT(*) FROM river_job WHERE kind='send' AND state IN ('available','pending','running','retryable','scheduled')").fetchone()[0]
        if count:
            raise ValueError(f'{count} unfinished River sends need draining or per-target reconciliation before export; job state was not changed')
    if 'outbox' in names:
        count = db.execute('SELECT COUNT(*) FROM outbox WHERE target_id IS NOT NULL AND sent_at IS NULL').fetchone()[0]
        if count:
            raise ValueError(f'{count} unfinished Worker sends need draining or per-target reconciliation before rollback export')


def literal(value):
    if value is None:
        return 'NULL'
    if isinstance(value, int):
        return str(value)
    if not isinstance(value, str) or '\x00' in value:
        raise ValueError('Unsupported SQLite value')
    return "'" + value.replace("'", "''") + "'"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['backup', 'export', 'manifest'])
    parser.add_argument('source')
    parser.add_argument('output', nargs='?')
    args = parser.parse_args()
    if args.command != 'manifest' and not args.output:
        parser.error('output is required')
    os.umask(0o077)
    with connect(args.source) as db:
        if args.command == 'backup':
            output = Path(args.output)
            with output.open('xb'):
                pass
            try:
                with sqlite3.connect(output) as destination:
                    db.backup(destination)
                    check(destination)
            except BaseException:
                output.unlink()
                raise
            print('Consistent SQLite backup created; integrity and foreign keys passed.')
            return
        db.execute('BEGIN')
        tables, manifest = rows_and_manifest(db)
        if args.command == 'manifest':
            print(json.dumps(manifest, indent=2))
            return
        pending(db)
        manifest_path = Path(args.output + '.manifest.json')
        if manifest_path.exists():
            raise ValueError('Manifest already exists; choose a new output path')
        statements = []
        for table, rows in tables.items():
            columns = ','.join(TABLES[table])
            for row in rows:
                statement = f"INSERT INTO {table}({columns}) VALUES({','.join(literal(v) for v in row)});\n"
                if len(statement.encode()) > 95000:
                    raise ValueError('Row exceeds the safe D1 statement size; import needs a reviewed chunked transfer')
                statements.append(statement)
        with Path(args.output).open('x', encoding='utf-8') as out:
            out.write('CREATE TABLE migration_import_guard (n INTEGER CHECK(n=0));\n')
            counts = '+'.join(f'(SELECT COUNT(*) FROM {table})' for table in TABLES)
            out.write(f'INSERT INTO migration_import_guard VALUES({counts});\nDROP TABLE migration_import_guard;\n')
            out.writelines(statements)
        with manifest_path.open('x', encoding='utf-8') as out:
            json.dump(manifest, out, indent=2)
        print('Application-only export created. Compare the manifest after import. No jobs, credentials or Litestream state were exported.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, sqlite3.Error, OSError) as error:
        print(f'Transfer refused: {error}', file=sys.stderr)
        sys.exit(1)
