#!/usr/bin/env node
// 按本次 YSS_SOURCE_SNAPSHOT 解析出的模板提交生成内嵌归档 internal/bundle/assets/bundles.json.gz。
// 归档不进入版本库：编译 CLI 前先运行本脚本；结果必须与已提交的 bundles.json.gz.sha256 一致。
//   node tools/fetch-bundle.mjs [--source-root DIR] [--force]
// --source-root 缺省为 $XDG_CACHE_HOME/yss-cli/template-source-<来源锁摘要>（或 ~/.cache/yss-cli/），
// 目录不存在时自动初始化并按固定提交浅取；已有模板仓时直接复用，缺少固定提交才联网取。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {gunzipSync} from 'node:zlib';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {fixedGoEnvironment} from './go-environment.mjs';

const TEMPLATE_REMOTE = 'https://github.com/iloveZzz/yss-spec-project-template.git';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const ARCHIVE = path.join(root, 'internal/bundle/assets/bundles.json.gz');
const DIGEST = ARCHIVE + '.sha256';
const COMMIT = /^[a-f0-9]{40}$/;
const sha = data => createHash('sha256').update(data).digest('hex');
const git = (cwd, args, options = {}) => (execFileSync('git', args, {cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'], ...options}) ?? '').trim();

export function expectedDigest(file = DIGEST) {
  const text = fs.readFileSync(file, 'utf8').trim();
  const digest = text.split(/\s+/)[0];
  if (!/^[a-f0-9]{64}$/.test(digest)) throw new Error(`摘要文件格式错误: ${DIGEST}`);
  return digest;
}

function archiveMatchesSnapshot(archive, lock) {
  try {
    const stored = JSON.parse(gunzipSync(fs.readFileSync(archive),{maxOutputLength:512*1024*1024}));
    const names = Object.keys(lock.profiles).sort();
    return JSON.stringify(Object.keys(stored.profiles).sort()) === JSON.stringify(names) && names.every(name => {
      const p=stored.profiles[name], s=lock.profiles[name];
      return p.templateCommit===s.templateCommit && p.templateVersion===s.templateVersion && p.sourceState===s.sourceState && p.sourcePolicy?.path===s.policyPath && p.sourcePolicy?.digest===s.policyHash && JSON.stringify(p.producer)===JSON.stringify(lock.producer) && JSON.stringify(p.legacy)===JSON.stringify(s.legacy);
    });
  } catch {return false}
}

function hasCommit(repo, commit) {
  try {git(repo, ['cat-file', '-e', commit + '^{commit}']); return true} catch {return false}
}

function ensureSources(sourceRoot, lock, readOnly = false) {
  if (!fs.existsSync(path.join(sourceRoot, '.git'))) {
    if (readOnly) throw new Error('来源仓库未初始化');
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
        if (readOnly) throw new Error(`缺少固定提交: ${profile}`);
        process.stderr.write(`fetch ${profile} ${item.templateCommit.slice(0, 12)}\n`);
        git(repo, ['fetch', '--depth', '1', 'origin', item.templateCommit], {stdio: 'inherit'});
      }
      if (item.templateTag) {
        if (!/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(item.templateTag)) throw new Error('模板 tag 必须是稳定版本');
        const ref = `refs/tags/${item.templateTag}`;
        let tagged = '';
        try {tagged = git(repo, ['rev-parse', '--verify', '-q', ref + '^{commit}'])} catch {}
        if (!tagged) {
          if (readOnly) throw new Error(`缺少模板 tag ${item.templateTag}`);
          git(repo, ['fetch', '--depth', '1', 'origin', `${ref}:${ref}`], {stdio: 'inherit'});
          tagged = git(repo, ['rev-parse', ref + '^{commit}']);
        }
        if (tagged !== item.templateCommit) throw new Error(`模板 tag ${item.templateTag} 与固定提交不一致`);
      }
    }
  }
}

export function findManagedSource(cache, lockBytes, lock, requiredCommit) {
  const digest = sha(lockBytes);
  const prefix = `template-source-${digest}-`;
  if (!fs.existsSync(cache)) return undefined;
  for (const name of fs.readdirSync(cache).filter(name => name.startsWith(prefix) && !name.endsWith('.ready'))) {
    const candidate = path.join(cache, name);
    try {
      if (!fs.lstatSync(candidate).isDirectory() || !fs.lstatSync(candidate + '.ready').isFile() || fs.readFileSync(candidate + '.ready', 'utf8') !== digest) continue;
      ensureSources(candidate, lock, true);
      if (requiredCommit && !hasCommit(candidate, requiredCommit)) continue;
      return candidate;
    } catch (error) {
      process.stderr.write(`缓存不可复用，保留在 ${candidate}: ${error.message}\n`);
    }
  }
}

function prepareManagedSource(lockBytes, lock) {
  const cache = path.resolve(process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache'), 'yss-cli');
  const digest = sha(lockBytes);
  const prefix = `template-source-${digest}-`;
  fs.mkdirSync(cache, {recursive: true});
  const existing = findManagedSource(cache, lockBytes, lock);
  if (existing) return existing;
  const candidate = fs.mkdtempSync(path.join(cache, prefix));
  try {
    ensureSources(candidate, lock);
    fs.writeFileSync(candidate + '.ready', digest, {flag: 'wx'});
    return candidate;
  } catch (error) {
    throw new Error(`缓存准备失败，保留在 ${candidate}: ${error.message}`, {cause: error});
  }
}

export function fetchBundle({sourceRoot, force = false}) {
  const archive = ARCHIVE, digest = DIGEST, lockFile = process.env.YSS_SOURCE_SNAPSHOT;
  if (!lockFile || !path.isAbsolute(lockFile)) throw new Error('YSS_SOURCE_SNAPSHOT 必须指向本次构建的绝对路径来源快照');
  const lockBytes = fs.readFileSync(lockFile);
  const lock = JSON.parse(lockBytes.toString('utf8'));
  const expected = expectedDigest(digest);
  // Explicit source roots are checked even if a previous archive is present.
  if (!sourceRoot && !force && fs.existsSync(archive) && sha(fs.readFileSync(archive)) === expected && archiveMatchesSnapshot(archive,lock)) return {status: 'up-to-date', sha256: expected};
  if (sourceRoot != null) sourceRoot = path.resolve(sourceRoot);
  else sourceRoot = prepareManagedSource(lockBytes, lock);
  const stage = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), 'yss-bundle-'));
  try {
    // -tags yss_nobundle：生成工具自举编译，不需要归档已存在。
    ensureSources(sourceRoot, lock);
    const frozenLock = path.join(stage, 'source-lock.json');
    fs.writeFileSync(frozenLock, lockBytes);
    execFileSync('go', ['run', '-tags', 'yss_nobundle', './tools/bundle', '--source-root', sourceRoot, '--lock', frozenLock, '--out', stage], {cwd: root, env: fixedGoEnvironment(), stdio: ['ignore', 'inherit', 'inherit']});
    if (!fs.readFileSync(lockFile).equals(lockBytes)) throw new Error('构建期间来源快照发生变化');
    const built = path.join(stage, 'bundles.json.gz');
    const actual = sha(fs.readFileSync(built));
    if (actual !== expected) {
      throw new Error(`生成的归档摘要与 ${path.relative(root, digest)} 不一致\n  期望 ${expected}\n  实际 ${actual}\n` +
        '固定来源或 Go 工具链与记录不同；若确为有意刷新来源，请运行 node tools/artifacts.mjs generate --scope all 更新摘要。');
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
      else if (args[i] === '--source-root' && args[i + 1]) options.sourceRoot = args[++i];
      else throw new Error('usage: fetch-bundle.mjs [--source-root DIR] [--force]');
    }
    const result = fetchBundle(options);
    const {verifyTrustedRegistry} = await import('./trusted-registry.mjs');
    verifyTrustedRegistry(root,fs.readFileSync(ARCHIVE));
    console.log(JSON.stringify(result));
  } catch (error) {console.error(error.message); process.exitCode = 1}
}
