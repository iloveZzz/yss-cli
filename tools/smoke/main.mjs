#!/usr/bin/env node
// 对已构建 yss 二进制的黑盒冒烟：不是单元测试，也不是发行验收。
//   node tools/smoke/main.mjs --bin <二进制路径> [--only profiles|git] [--keep]
// 失败时以非 0 退出；环境（git 配置、HOME、PATH）全部隔离在临时目录，不触碰用户全局配置。
import fs from 'node:fs';
import {makeTempRoot, Session, SmokeFailure} from './lib.mjs';
import {PROFILES, runProfile} from './profiles.mjs';
import {runGit} from './git.mjs';

function parseArgs(argv) {
  const options = {bin: '', only: '', keep: false};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--bin') options.bin = argv[++i] ?? '';
    else if (arg === '--only') options.only = argv[++i] ?? '';
    else if (arg === '--keep') options.keep = true;
    else throw new Error(`未知参数: ${arg}`);
  }
  if (!options.bin) throw new Error('需要 --bin <被测二进制路径>');
  if (!['', 'profiles', 'git'].includes(options.only)) throw new Error('--only 只能是 profiles 或 git');
  return options;
}

let options;
try {options = parseArgs(process.argv.slice(2))} catch (error) {
  process.stderr.write(`${error.message}\n用法: node tools/smoke/main.mjs --bin <二进制> [--only profiles|git] [--keep]\n`);
  process.exit(2);
}

const tempRoot = makeTempRoot();
let failed = false;
try {
  const session = new Session({binary: options.bin, tempRoot});
  const version = session.expectOk(['version', '--json'], {label: 'version'});
  console.log(`被测二进制 yss ${version.version}（临时目录 ${tempRoot}）`);
  if (options.only !== 'git') for (const profile of PROFILES) runProfile(session, profile, tempRoot);
  if (options.only !== 'profiles') runGit(session, tempRoot);
  console.log('SMOKE PASS');
} catch (error) {
  failed = true;
  console.error(`\nSMOKE FAIL: ${error instanceof SmokeFailure ? error.message : error.stack ?? error}`);
} finally {
  if (options.keep || failed) console.error(`临时目录保留: ${tempRoot}`);
  else fs.rmSync(tempRoot, {recursive: true, force: true});
}
process.exit(failed ? 1 : 0);
