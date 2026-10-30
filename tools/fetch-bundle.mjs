#!/usr/bin/env node
// 按 docs/source-lock.json 的固定模板提交生成内嵌归档 internal/bundle/assets/bundles.json.gz。
// 归档不进入版本库：编译 CLI 前先运行本脚本；结果必须与已提交的 bundles.json.gz.sha256 一致。
//   node tools/fetch-bundle.mjs [--source-root DIR] [--force] [--baseline]
// --source-root 缺省为 $XDG_CACHE_HOME/yss-cli/template-source（或 ~/.cache/yss-cli/template-source），
// 目录不存在时自动初始化并按固定提交浅取；已有模板仓时直接复用，缺少固定提交才联网取。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {fixedGoEnvironment} from './go-environment.mjs';
import {createBaselineDelta} from './baseline-delta.mjs';

const TEMPLATE_REMOTE = 'https://github.com/iloveZzz/yss-spec-project-template.git';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const ARCHIVE = path.join(root, 'internal/bundle/assets/bundles.json.gz');
const DIGEST = ARCHIVE + '.sha256';
const DELTA = path.join(root, 'internal/bundle/assets/baseline-delta.json.gz');
const BASELINE_GENERATOR = 'd67bc777198174a92cc3f58e4f26ce9f0daef2d9';
const BASELINE_TAG_OBJECT = '0cd62adfc13edad5caa5efd1b5c15acbdbc5b6d7';
const BASELINE_TAG_SHA256 = '395512eb59b70a8ff76a7b3f466acb8943d4ea1b96faa16670a5c708f45f0a5e';
const BASELINE_TEMPLATE_COMMIT = '1e49127842c358caf552af8ebec58f438a35c5a6';
const COMMIT = /^[a-f0-9]{40}$/;
const sha = data => createHash('sha256').update(data).digest('hex');
const git = (cwd, args, options = {}) => (execFileSync('git', args, {cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'], ...options}) ?? '').trim();

export function expectedDigest(file = DIGEST) {
  const text = fs.readFileSync(file, 'utf8').trim();
  const digest = text.split(/\s+/)[0];
  if (!/^[a-f0-9]{64}$/.test(digest)) throw new Error(`摘要文件格式错误: ${DIGEST}`);
  return digest;
}

function hasCommit(repo, commit) {
  try {git(repo, ['cat-file', '-e', commit + '^{commit}']); return true} catch {return false}
}

function ensureSources(sourceRoot, lock) {
  if (!fs.existsSync(path.join(sourceRoot, '.git'))) {
    fs.mkdirSync(sourceRoot, {recursive: true});
    git(sourceRoot, ['init', '-q']);
    git(sourceRoot, ['remote', 'add', 'origin', TEMPLATE_REMOTE]);
  }
  for (const [profile, source] of Object.entries(lock.profiles)) {
    for (const item of [source, source.skillsSource].filter(Boolean)) {
      if (!COMMIT.test(item.templateCommit)) throw new Error(`需要完整的固定提交: ${profile}`);
      const repo = path.resolve(sourceRoot, item.sourcePath);
      if (path.relative(sourceRoot, repo).split(path.sep).includes('..')) throw new Error('sourcePath 越出 source-root');
      if (!fs.existsSync(repo) || git(repo, ['rev-parse', '--show-toplevel']) !== fs.realpathSync(repo)) {
        throw new Error(`来源仓库未初始化: ${repo}（子仓来源需自行初始化）`);
      }
      if (!hasCommit(repo, item.templateCommit)) {
        process.stderr.write(`fetch ${profile} ${item.templateCommit.slice(0, 12)}\n`);
        git(repo, ['fetch', '--depth', '1', 'origin', item.templateCommit], {stdio: 'inherit'});
      }
      if (item.templateTag) {
        if (!/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(item.templateTag)) throw new Error('模板 tag 必须是稳定版本');
        const ref = `refs/tags/${item.templateTag}`;
        let tagged = '';
        try {tagged = git(repo, ['rev-parse', '--verify', '-q', ref + '^{commit}'])} catch {}
        if (!tagged) {
          git(repo, ['fetch', '--depth', '1', 'origin', `${ref}:${ref}`], {stdio: 'inherit'});
          tagged = git(repo, ['rev-parse', ref + '^{commit}']);
        }
        if (tagged !== item.templateCommit) throw new Error(`模板 tag ${item.templateTag} 与固定提交不一致`);
      }
    }
  }
}

export function extractBaselineGenerator(repo, directory) {
  let fixedSource;
  try {
    fixedSource = execFileSync('git', ['archive', BASELINE_GENERATOR], {cwd: repo, maxBuffer: 256 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe']});
  } catch {
    throw new Error(`缺少固定 CLI 3.0 源码提交 ${BASELINE_GENERATOR}；请先从受信来源取得该 Git 对象，不能回退到当前生成器。`);
  }
  execFileSync('tar', ['-x', '-C', directory], {input: fixedSource});
}

export function prepareBaselineSource(sourceRoot, directory, tagObject, expectedTag, expectedCommit, expectedSha256) {
  const objectHash = createHash('sha1').update(`tag ${tagObject.length}\0`).update(tagObject).digest('hex');
  if (objectHash !== expectedTag || sha(tagObject) !== expectedSha256) throw new Error('历史标签对象摘要不一致');
  if (!tagObject.toString('utf8').startsWith(`object ${expectedCommit}\ntype commit\ntag v5.0.0\n`)) {
    throw new Error('历史标签与固定提交不一致');
  }
  if (fs.existsSync(sourceRoot) && !fs.statSync(sourceRoot).isDirectory()) throw new Error('模板来源不是目录');
  let source = TEMPLATE_REMOTE;
  if (fs.existsSync(path.join(sourceRoot, '.git'))) {
    source = hasCommit(sourceRoot, expectedCommit) ? sourceRoot : git(sourceRoot, ['remote', 'get-url', 'origin']);
  }
  fs.mkdirSync(directory);
  git(directory, ['init', '-q']);
  git(directory, ['fetch', '--depth', '1', '--no-tags', source, expectedCommit], {stdio: 'inherit'});
  const restored = git(directory, ['hash-object', '-t', 'tag', '-w', '--stdin'], {input: tagObject, stdio: ['pipe', 'pipe', 'inherit']});
  if (restored !== expectedTag) throw new Error('历史标签对象写入不一致');
  git(directory, ['update-ref', 'refs/tags/v5.0.0', restored]);
  if (git(directory, ['rev-parse', 'refs/tags/v5.0.0^{commit}']) !== expectedCommit) throw new Error('历史标签与固定提交不一致');
}

export function fetchBundle({sourceRoot, force = false, baseline = false}) {
  const archive = baseline ? path.join(root, 'internal/bundle/assets/baseline-5.0.0.json.gz') : ARCHIVE;
  const digest = baseline ? archive + '.sha256' : DIGEST;
  const lockFile = baseline ? 'docs/template-5.0.0-source-lock.json' : 'docs/source-lock.json';
  const expected = expectedDigest(digest);
  if (!force && fs.existsSync(archive) && sha(fs.readFileSync(archive)) === expected) return {status: 'up-to-date', sha256: expected};
  sourceRoot = path.resolve(sourceRoot ?? path.join(process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache'), 'yss-cli', 'template-source'));
  const lock = JSON.parse(fs.readFileSync(path.join(root, lockFile), 'utf8'));
  const stage = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), 'yss-bundle-'));
  try {
    // -tags yss_nobundle：生成工具自举编译，不需要归档已存在。
    let generatorRoot = root;
    if (baseline) {
      generatorRoot = path.join(stage, 'generator');
      fs.mkdirSync(generatorRoot);
      extractBaselineGenerator(root, generatorRoot);
      if (!fs.readFileSync(path.join(generatorRoot, 'docs/source-lock.json')).equals(fs.readFileSync(path.join(root, lockFile))) ||
          expectedDigest(path.join(generatorRoot, 'internal/bundle/assets/bundles.json.gz.sha256')) !== expected) {
        throw new Error('受信基线锁或摘要与固定 CLI 3.0 来源不一致');
      }
      const historicalRoot = path.join(stage, 'historical-source');
      prepareBaselineSource(sourceRoot, historicalRoot, fs.readFileSync(path.join(root, 'docs/template-5.0.0-tag-object.txt')), BASELINE_TAG_OBJECT, BASELINE_TEMPLATE_COMMIT, BASELINE_TAG_SHA256);
      sourceRoot = historicalRoot;
    }
    ensureSources(sourceRoot, lock);
    execFileSync('go', ['run', '-tags', 'yss_nobundle', './tools/bundle', '--source-root', sourceRoot, '--lock', path.join(root, lockFile), '--out', stage], {cwd: generatorRoot, env: fixedGoEnvironment(), stdio: ['ignore', 'inherit', 'inherit']});
    const built = path.join(stage, 'bundles.json.gz');
    const actual = sha(fs.readFileSync(built));
    if (actual !== expected) {
      throw new Error(`生成的归档摘要与 ${path.relative(root, digest)} 不一致\n  期望 ${expected}\n  实际 ${actual}\n` +
        (baseline ? '固定基线不得改写；请核对固定来源与 Go 工具链。' : '固定来源或 Go 工具链与记录不同；若确为有意刷新来源，请运行 node tools/artifacts.mjs generate --scope all 更新摘要。'));
    }
    const temporary = archive + `.${process.pid}.tmp`;
    fs.copyFileSync(built, temporary);
    fs.renameSync(temporary, archive);
    return {status: 'generated', sha256: actual};
  } finally {fs.rmSync(stage, {recursive: true, force: true})}
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = {};
    const args = process.argv.slice(2);
    for (let i = 0; i < args.length; i++) {
      if (args[i] === '--force') options.force = true;
      else if (args[i] === '--baseline') options.baseline = true;
      else if (args[i] === '--source-root' && args[i + 1]) options.sourceRoot = args[++i];
      else throw new Error('usage: fetch-bundle.mjs [--source-root DIR] [--force] [--baseline]');
    }
    const result = fetchBundle(options);
    if (!options.baseline) {
      fetchBundle({...options, baseline: true});
      const delta = createBaselineDelta(fs.readFileSync(ARCHIVE), fs.readFileSync(path.join(root, 'internal/bundle/assets/baseline-5.0.0.json.gz')));
      const temporary = DELTA + `.${process.pid}.tmp`;
      try { fs.writeFileSync(temporary, delta, {flag:'wx'}); fs.renameSync(temporary, DELTA); }
      finally { if (fs.existsSync(temporary)) fs.unlinkSync(temporary); }
    }
    console.log(JSON.stringify(result));
  } catch (error) {console.error(error.message); process.exitCode = 1}
}
