'use strict';
// Discover the complete frontend suite so a new view cannot be omitted from CI.
const fs=require('node:fs'),path=require('node:path'),cp=require('node:child_process');
const repo=path.resolve(__dirname,'..');
function run(args){const r=cp.spawnSync(process.execPath,args,{cwd:repo,encoding:'utf8',windowsHide:true});if(r.status!==0){process.stderr.write(r.stdout||'');process.stderr.write(r.stderr||'');if(r.error)console.error(r.error.message);process.exit(r.status||1);}return r.stdout;}
const assets=fs.readdirSync(path.join(repo,'web/assets')).filter(f=>f.endsWith('.js')).sort();
for(const f of assets)run(['--check','web/assets/'+f]);
const tests=fs.readdirSync(path.join(repo,'web/tests')).filter(f=>f.endsWith('.cjs')).sort();
for(const f of tests){process.stdout.write(run(['web/tests/'+f]));process.stdout.write(f+': PASS\n');}
console.log(`Frontend complete: ${assets.length} syntax checks, ${tests.length} state suites`);
