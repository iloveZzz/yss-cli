#!/usr/bin/env node
// yss scaffold 的 Go 实现与模板仓 Node 生成器逐字节对拍。构建后黑盒校验，不是单元测试，也不代表发行验收。
//   node tools/scaffold-parity/main.mjs --bin <yss 二进制> --template-root <固定提交的模板仓> [--only id,id] [--list] [--keep] [--work-dir <新目录>]
// 仅用于开发与 CI；Node 在此只是对拍基准，yss 运行时不依赖 Node。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {ParityFailure, REGISTRY, compareSnapshots, git, run, sha256, snapshot} from './lib.mjs';
import {flags, standaloneScenarios} from './scenarios.mjs';
import {formalScenarios} from './formal.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '../..');
const SKILLS = {ddd: 'yss-ddd-scaffold-generator', mvc: 'yss-layered-mvc-scaffold-generator'};

function parseArgs(argv) {
  const options = {bin: '', templateRoot: '', only: [], keep: false, list: false, workDir: ''};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--bin') options.bin = argv[++i] ?? '';
    else if (arg === '--template-root') options.templateRoot = argv[++i] ?? '';
    else if (arg === '--work-dir') options.workDir = argv[++i] ?? '';
    else if (arg === '--only') options.only = (argv[++i] ?? '').split(',').filter(Boolean);
    else if (arg === '--keep') options.keep = true;
    else if (arg === '--list') options.list = true;
    else throw new Error(`未知参数: ${arg}`);
  }
  if (!options.templateRoot) throw new Error('需要 --template-root <固定提交的模板仓>');
  if (!options.list && !options.bin) throw new Error('需要 --bin <被测二进制路径>');
  return options;
}

// 基准必须是 docs/source-lock.json 固定的模板提交，且工作区干净；否则对拍结论无意义。
function verifyTemplate(templateRoot) {
  const lock = JSON.parse(fs.readFileSync(path.join(repoRoot, 'docs/source-lock.json'), 'utf8'));
  const expected = lock.profiles.spec.templateCommit;
  const head = git(templateRoot, ['rev-parse', 'HEAD']);
  if (head !== expected) throw new ParityFailure(`模板仓提交 ${head} 不是固定提交 ${expected}`);
  const dirty = git(templateRoot, ['status', '--porcelain', '--untracked-files=no']);
  if (dirty) throw new ParityFailure(`模板仓工作区不干净:\n${dirty}`);
  return expected;
}

// Go 实现读取的是内嵌 Bundle；Node 基准读取模板仓。凡是影响输出的输入（技能目录、指纹绑定的库文件、
// 平台目录与证据、下游技能摘要来源）必须逐字节、逐可执行位一致，否则对拍结论无意义。
const OUTPUT_FILES = [
  'scripts/lib/backend-platform.mjs', 'scripts/lib/scaffold-local-database.mjs', 'scripts/lib/standalone-backend-scaffold.mjs',
  'scripts/lib/scaffold-reference-example.mjs', 'scripts/lib/backend-platform-provenance.mjs', 'scripts/lib/backend-platform-verification.mjs',
  'scripts/lib/command-runner.mjs', 'scripts/vendor/xml.mjs',
  '.agents/skills/yss-dto/references/openapi-wire-profile.yaml',
  '.agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml',
  '.template-spec/agents/backend-architecture-profiles.md',
];
const OUTPUT_DIRS = [
  ...Object.values(SKILLS).map(skill => `.agents/skills/${skill}`),
  '.template-spec/engineering',
  ...['yss-domain', 'yss-application', 'yss-repository', 'yss-mybatis', 'yss-web-controller', 'yss-dto', 'yss-exception', 'yss-validation', 'mapstruct', 'lombok', 'alibaba-java-code-style'].map(skill => `.agents/skills/${skill}`),
];

function walkFiles(root, relative) {
  const out = [];
  const visit = current => {
    for (const entry of fs.readdirSync(path.join(root, current), {withFileTypes: true})) {
      const next = `${current}/${entry.name}`;
      if (entry.isDirectory()) visit(next);
      else out.push(next);
    }
  };
  visit(relative);
  return out;
}

function verifyBundle(context) {
  const exported = path.join(context.work, 'bundle-export');
  const result = run(context.bin, ['bundle', 'export', '--profile', 'spec', '--out', exported], {env: context.env});
  if (result.status !== 0) throw new ParityFailure(`bundle export 失败: ${result.stderr || result.stdout}`);
  const files = new Set(OUTPUT_FILES);
  for (const directory of OUTPUT_DIRS) if (fs.existsSync(path.join(context.templateRoot, directory))) walkFiles(context.templateRoot, directory).forEach(file => files.add(file));
  let checked = 0;
  for (const file of [...files].sort()) {
    const left = path.join(context.templateRoot, file);
    const right = path.join(exported, file);
    if (!fs.existsSync(right)) throw new ParityFailure(`内嵌 Bundle 缺少影响输出的输入: ${file}`);
    if (sha256(fs.readFileSync(left)) !== sha256(fs.readFileSync(right))) throw new ParityFailure(`内嵌 Bundle 与模板仓不一致: ${file}`);
    if ((fs.statSync(left).mode & 0o111) !== (fs.statSync(right).mode & 0o111)) throw new ParityFailure(`内嵌 Bundle 与模板仓的可执行位不一致: ${file}`);
    checked++;
  }
  fs.rmSync(exported, {recursive: true, force: true});
  return checked;
}

function toDriverOptions(scenario, outputDir) {
  const options = {architecture: scenario.arch, outputDir, ...scenario.options};
  if (scenario.kind === 'standalone') options.withExample = Boolean(scenario.example);
  return options;
}

function runNode(context, scenario, outputDir) {
  const script = path.join(context.templateRoot, '.agents/skills', SKILLS[scenario.arch], 'scripts/generate_scaffold.mjs');
  const args = [script, ...flags({...scenario.options, outputDir})];
  // 旧 CLI 对独立生成追加 --without-example；启用示例的场景显式传 --with-example。
  if (scenario.kind === 'standalone') args.push(scenario.example ? '--with-example' : '--without-example');
  return run(process.execPath, args, {env: context.env});
}

function runGo(context, scenario, outputDir) {
  if (scenario.example) {
    const optionsFile = path.join(context.work, `${scenario.id}.driver.json`);
    fs.writeFileSync(optionsFile, JSON.stringify(toDriverOptions(scenario, outputDir)));
    return run(context.driver, ['--options', optionsFile], {env: context.env});
  }
  return run(context.bin, ['scaffold', scenario.arch, ...flags({...scenario.options, outputDir}), '--json'], {env: context.env});
}

function buildDriver(context) {
  context.driver = path.join(context.work, 'driver');
  const result = run('go', ['build', '-o', context.driver, './tools/scaffold-parity/driver'], {cwd: repoRoot, env: {...process.env, CGO_ENABLED: '0'}});
  if (result.status !== 0) throw new ParityFailure(`构建对拍驱动失败: ${result.stderr}`);
}

function playScenario(context, scenario) {
  const base = path.join(context.work, 'scenarios', scenario.id);
  const out = path.join(base, 'out');
  const outputDir = scenario.outputSubdir ? path.join(out, scenario.outputSubdir) : out;
  const results = {};
  const window = {start: Date.now(), end: 0};
  for (const side of ['node', 'go']) {
    fs.rmSync(out, {recursive: true, force: true});
    fs.mkdirSync(out, {recursive: true});
    scenario.setup?.(out, side, context);
    results[side] = (side === 'node' ? runNode : runGo)(context, scenario, outputDir);
    fs.renameSync(out, path.join(base, `out-${side}`));
  }
  window.end = Date.now();
  const problems = [];
  const succeeded = side => results[side].status === 0;
  const expected = scenario.expect === 'success';
  for (const side of ['node', 'go']) {
    if (succeeded(side) !== expected) {
      problems.push(`${side === 'node' ? 'Node' : 'Go'} ${succeeded(side) ? '意外成功' : `意外失败(退出码 ${results[side].status})`}: ${(results[side].stderr || results[side].stdout).trim().split('\n').slice(-3).join(' | ')}`);
    }
  }
  const used = new Set();
  const exclude = new Set(scenario.exclude ?? []);
  const view = side => {
    const map = snapshot(path.join(base, `out-${side}`));
    for (const key of [...map.keys()]) if ([...exclude].some(name => key === name || key.startsWith(`${name}/`))) map.delete(key);
    return map;
  };
  const compared = compareSnapshots(view('node'), view('go'), window, used);
  problems.push(...compared.diffs);
  if (expected && succeeded('go')) {
    // CLI 返回的 JSON 信封须与清单一致（驱动场景不经 CLI）。
    if (!scenario.example) problems.push(...checkEnvelope(results.go.stdout, outputDir, scenario));
  }
  return {problems, compared, used, results};
}

function checkEnvelope(stdout, outputDir, scenario) {
  let envelope;
  try {envelope = JSON.parse(stdout)} catch {return ['Go CLI 的 --json 输出不是合法 JSON']}
  const result = envelope.result ?? {};
  const problems = [];
  const root = path.join(outputDir, scenario.options.projectName);
  if (envelope.status !== 'ok') problems.push(`CLI 状态 ${envelope.status}`);
  if (result.projectRoot !== root) problems.push(`projectRoot=${result.projectRoot}，期望 ${root}`);
  if (result.manifestPath !== path.join(root, '.yss/scaffold-generation.json')) problems.push(`manifestPath=${result.manifestPath}`);
  if (result.generatorSkill !== SKILLS[scenario.arch] || result.architecture !== scenario.arch) problems.push('generatorSkill/architecture 不符');
  if (scenario.kind === 'standalone' && (result.generationMode !== 'standalone-generation' || result.completionLevel !== 'generated' || result.verification?.status !== 'not-executed')) problems.push(`独立模式摘要不符: ${JSON.stringify(result)}`);
  if (!/^[a-f0-9]{64}$/.test(result.bundleHash ?? '')) problems.push('bundleHash 缺失');
  return problems;
}

function checkNodeLocale(env) {
  const result = run(process.execPath, ['-e', 'process.stdout.write(new Intl.Collator().resolvedOptions().locale)'], {env});
  if (result.stdout !== 'en-US') throw new ParityFailure(`Node 的 ICU 区域是 ${JSON.stringify(result.stdout)}，对拍要求 en-US（Node 生成器的 localeCompare 依赖区域）`);
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  process.umask(0o022);
  const templateRoot = fs.realpathSync(options.templateRoot);
  const catalog = JSON.parse(fs.readFileSync(path.join(templateRoot, '.template-spec/engineering/backend-platforms.json'), 'utf8'));
  const standalone = standaloneScenarios(catalog);
  const work = options.workDir ? path.resolve(options.workDir) : fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'yss-scaffold-parity-')));
  if (options.workDir) {
    if (fs.existsSync(work)) throw new Error(`--work-dir 必须是不存在的新目录: ${work}`);
    fs.mkdirSync(work, {recursive: true});
  }
  const context = {templateRoot, work: fs.realpathSync(work)};
  const formal = await formalScenarios(context, {templateRoot, work: context.work, repoRoot});
  const scenarios = [...standalone, ...formal];
  if (options.list) {
    for (const scenario of scenarios) console.log(`${scenario.id}\t${scenario.expect}\t${scenario.title}`);
    fs.rmSync(work, {recursive: true, force: true});
    return 0;
  }
  const selected = options.only.length ? scenarios.filter(item => options.only.some(pattern => item.id === pattern || item.id.startsWith(pattern))) : scenarios;
  if (!selected.length) throw new Error('没有匹配的场景');

  context.bin = fs.realpathSync(options.bin);
  const home = path.join(context.work, 'home');
  fs.mkdirSync(home, {recursive: true});
  fs.writeFileSync(path.join(home, '.gitconfig'), '[user]\n\tname = Parity\n\temail = parity@example.invalid\n[init]\n\tdefaultBranch = main\n');
  // 两侧使用同一环境：固定区域、用户名和时区；原生治理校验（正式模式）指向被测二进制。
  context.env = {
    PATH: process.env.PATH ?? '', HOME: home, LANG: 'en_US.UTF-8', LC_ALL: 'en_US.UTF-8', USER: 'scaffold-parity', TZ: 'UTC',
    GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: path.join(home, '.gitconfig'), GIT_TERMINAL_PROMPT: '0',
    YSS_NATIVE_BINARY: context.bin,
  };
  const commit = verifyTemplate(templateRoot);
  checkNodeLocale(context.env);
  const closure = verifyBundle(context);
  console.log(`被测二进制 ${context.bin}\n模板基准 ${templateRoot} @ ${commit.slice(0, 12)}（Bundle 输入闭包 ${closure} 个文件逐字节一致）\n工作目录 ${context.work}`);
  if (selected.some(item => item.example)) buildDriver(context);

  let failed = 0;
  const used = new Set();
  let totals = {files: 0, bytes: 0};
  for (const scenario of selected) {
    const started = Date.now();
    let outcome;
    try {outcome = playScenario(context, scenario)} catch (error) {outcome = {problems: [error instanceof ParityFailure ? error.message : error.stack], compared: {files: 0, bytes: 0, entries: 0}, used: new Set(), results: {}}}
    outcome.used.forEach(id => used.add(id));
    totals = {files: totals.files + outcome.compared.files, bytes: totals.bytes + outcome.compared.bytes};
    const ok = outcome.problems.length === 0;
    if (!ok) failed++;
    console.log(`${ok ? 'PASS' : 'FAIL'}  ${scenario.id}  ${scenario.expect === 'success' ? `${outcome.compared.files} 文件 ${outcome.compared.bytes} 字节` : '双方均拒绝且无产物差异'}  ${Date.now() - started}ms`);
    for (const problem of outcome.problems) console.log(`      ${problem}`);
  }
  console.log(`\n场景 ${selected.length}，失败 ${failed}；比较文件 ${totals.files}，字节 ${totals.bytes}`);
  console.log(`登记差异使用: ${[...used].join(', ') || '无'}（登记表共 ${REGISTRY.length} 项：${REGISTRY.map(item => item.id).join(', ')}）`);
  console.log('说明：对拍为构建后黑盒校验，不代表发行验收。');
  if (options.keep || failed) console.log(`工作目录保留: ${context.work}`);
  else fs.rmSync(context.work, {recursive: true, force: true});
  return failed ? 1 : 0;
}

main().then(code => process.exit(code), error => {
  console.error(`PARITY ERROR: ${error instanceof ParityFailure ? error.message : error.stack}`);
  process.exit(2);
});
