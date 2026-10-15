#!/usr/bin/env node
// 按 docs/source-lock.json 的固定模板提交生成内嵌归档 internal/bundle/assets/bundles.json.gz。
// 归档不进入版本库：编译 CLI 前先运行本脚本；结果必须与已提交的 bundles.json.gz.sha256 一致。
//   node tools/fetch-bundle.mjs [--source-root DIR] [--force]
// --source-root 缺省为 $XDG_CACHE_HOME/yss-cli/template-source（或 ~/.cache/yss-cli/template-source），
// 目录不存在时自动初始化并按固定提交浅取；已有模板仓时直接复用，缺少固定提交才联网取。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';
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

export function expectedDigest() {
  const text = fs.readFileSync(DIGEST, 'utf8').trim();
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

export function fetchBundle({sourceRoot, force = false}) {
  const expected = expectedDigest();
  if (!force && fs.existsSync(ARCHIVE) && sha(fs.readFileSync(ARCHIVE)) === expected) return {status: 'up-to-date', sha256: expected};
  sourceRoot = path.resolve(sourceRoot ?? path.join(process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache'), 'yss-cli', 'template-source'));
  const lock = JSON.parse(fs.readFileSync(path.join(root, 'docs/source-lock.json'), 'utf8'));
  ensureSources(sourceRoot, lock);
  const stage = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), 'yss-bundle-'));
  try {
    // -tags yss_nobundle：生成工具自举编译，不需要归档已存在。
    execFileSync('go', ['run', '-tags', 'yss_nobundle', './tools/bundle', '--source-root', sourceRoot, '--lock', 'docs/source-lock.json', '--out', stage], {cwd: root, env: fixedGoEnvironment(), stdio: ['ignore', 'inherit', 'inherit']});
    const built = path.join(stage, 'bundles.json.gz');
    const actual = sha(fs.readFileSync(built));
    if (actual !== expected) {
      throw new Error(`生成的归档摘要与 ${path.relative(root, DIGEST)} 不一致\n  期望 ${expected}\n  实际 ${actual}\n` +
        '固定来源或 Go 工具链与记录不同；若确为有意刷新来源，请运行 node tools/artifacts.mjs generate --scope all 更新摘要。');
    }
    const temporary = ARCHIVE + `.${process.pid}.tmp`;
    fs.copyFileSync(built, temporary);
    fs.renameSync(temporary, ARCHIVE);
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
    console.log(JSON.stringify(fetchBundle(options)));
  } catch (error) {console.error(error.message); process.exitCode = 1}
}
