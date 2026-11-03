import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { execFileSync } from 'node:child_process';

const [account, database, output] = process.argv.slice(2);
if (!/^[a-f0-9]{32}$/.test(account ?? '') || !/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(database ?? '') || database === '00000000-0000-0000-0000-000000000000' || !output) {
  throw new Error('Usage: npm run prepare:cloudflare -- ACCOUNT_ID D1_DATABASE_ID OUTPUT.sql (real IDs required)');
}
const config = JSON.parse(readFileSync('wrangler.jsonc', 'utf8'));
if (account !== config.account_id) throw new Error('Refusing preparation: account ID must match wrangler.jsonc.');
const directory = mkdtempSync(join(tmpdir(), 'calendar-digest-upload-'));
try {
  execFileSync(resolve('node_modules/.bin/wrangler'), ['deploy', '--dry-run', '--outdir', directory], { stdio: 'inherit' });
  const modules = readdirSync(directory).filter(name => /\.(js|txt)$/.test(name));
  if (!modules.includes('index.js')) throw new Error('Expected Wrangler to emit index.js');
  const metadata = {
    main_module: 'index.js', compatibility_date: config.compatibility_date,
    bindings: [
      { type: 'd1', name: 'DB', id: database },
      ...Object.entries(config.vars).map(([name, text]) => ({ type: 'plain_text', name, text })),
    ],
    keep_bindings: ['secret_text'],
    observability: config.observability,
  };
  const payload = {
    account, script: config.name, team: config.vars.ACCESS_TEAM_DOMAIN, metadata,
    modules: modules.map(name => ({
      name, type: name === 'index.js' ? 'application/javascript+module' : 'text/plain',
      content: readFileSync(join(directory, name), 'utf8'),
    })),
  };
  const json = JSON.stringify(payload);
  const sql = `CREATE TABLE _amp_deployment_bundle (payload TEXT NOT NULL);\nINSERT INTO _amp_deployment_bundle VALUES ('${json.replaceAll("'", "''")}');\n`;
  if (Buffer.byteLength(sql) > 95_000) throw new Error('Bundle exceeds the D1 single-statement transfer limit. Use a reviewed chunked upload.');
  writeFileSync(output, sql, { flag: 'wx', mode: 0o600 });
  console.log(JSON.stringify({ output, importMD5: createHash('md5').update(sql).digest('hex'), bundleSHA256: createHash('sha256').update(json).digest('hex') }));
  console.log('Prepared only; no deployment. After approval, transfer through the D1 import API, read the disposable bundle table in cloudflare.execute, verify SHA256, then upload its modules. Drop the scratch table after success. Cron and routes are separate operations. See docs/workers.md.');
} finally {
  rmSync(directory, { recursive: true, force: true });
}
