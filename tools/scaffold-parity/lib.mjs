// 对拍公共工具：目录快照、逐项比较、登记差异、子进程运行。仅开发/CI 使用，不进入发行包。
import {spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

export class ParityFailure extends Error {}

export const sha256 = data => createHash('sha256').update(data).digest('hex');

// 登记差异：唯一允许放行的差异来源。每一项必须说明路径、原因，并只改写它声明的片段；
// 改写后两侧必须一致，且两侧都必须真的匹配（缺席或多处匹配都算失败）。
export const REGISTRY = [
  {
    id: 'generated-at',
    path: '.yss/scaffold-generation.json',
    reason: '清单 generated_at 是生成时刻（UTC，毫秒，Z），每次运行必然不同',
    pattern: /^(\s*"generated_at": )"(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z)"/m,
    // 值必须落在本次对拍进行期间，不能是任意字符串。
    check(value, window) {
      const time = Date.parse(value);
      return time >= window.start - 5000 && time <= window.end + 5000;
    },
    neutral: '$1"<generated_at>"',
  },
];

export function applyRegistry(relative, buffer, window, used) {
  let text = null;
  for (const entry of REGISTRY) {
    if (!(relative === entry.path || relative.endsWith('/' + entry.path))) continue;
    text ??= buffer.toString('utf8');
    const match = entry.pattern.exec(text);
    if (!match) throw new ParityFailure(`登记差异 ${entry.id} 在 ${relative} 中未匹配`);
    if (!entry.check(match[2], window)) throw new ParityFailure(`登记差异 ${entry.id} 的值不在对拍窗口内: ${match[2]}`);
    if (new RegExp(entry.pattern.source, 'gm').exec(text.slice(match.index + match[0].length))) {
      throw new ParityFailure(`登记差异 ${entry.id} 在 ${relative} 中出现多次`);
    }
    text = text.replace(entry.pattern, entry.neutral);
    used.add(entry.id);
  }
  return text === null ? buffer : Buffer.from(text, 'utf8');
}

// 递归快照：文件集合、类型、权限位、内容 SHA-256、符号链接目标（含空目录）。
export function snapshot(root) {
  const entries = new Map();
  const visit = (directory, prefix) => {
    for (const name of fs.readdirSync(directory).sort()) {
      const full = path.join(directory, name);
      const relative = prefix ? `${prefix}/${name}` : name;
      const info = fs.lstatSync(full);
      const mode = info.mode & 0o7777;
      if (info.isSymbolicLink()) entries.set(relative, {type: 'symlink', mode, target: fs.readlinkSync(full)});
      else if (info.isDirectory()) {
        entries.set(relative, {type: 'dir', mode});
        visit(full, relative);
      } else if (info.isFile()) entries.set(relative, {type: 'file', mode, size: info.size, full});
      else entries.set(relative, {type: 'other', mode});
    }
  };
  visit(root, '');
  return entries;
}

export function compareSnapshots(left, right, window, used) {
  const diffs = [];
  const names = [...new Set([...left.keys(), ...right.keys()])].sort();
  let files = 0;
  let bytes = 0;
  for (const name of names) {
    const a = left.get(name);
    const b = right.get(name);
    if (!a) { diffs.push(`仅 Go 产出: ${name}`); continue; }
    if (!b) { diffs.push(`仅 Node 产出: ${name}`); continue; }
    if (a.type !== b.type) { diffs.push(`类型不同 ${name}: Node=${a.type} Go=${b.type}`); continue; }
    if (a.mode !== b.mode) diffs.push(`权限不同 ${name}: Node=${a.mode.toString(8)} Go=${b.mode.toString(8)}`);
    if (a.type === 'symlink' && a.target !== b.target) diffs.push(`符号链接目标不同 ${name}: Node=${a.target} Go=${b.target}`);
    if (a.type === 'file') {
      files++;
      const x = applyRegistry(name, fs.readFileSync(a.full), window, used);
      const y = applyRegistry(name, fs.readFileSync(b.full), window, used);
      bytes += x.length;
      if (sha256(x) !== sha256(y)) diffs.push(`内容不同 ${name}: Node sha256=${sha256(x).slice(0, 12)} Go sha256=${sha256(y).slice(0, 12)}${firstDifference(x, y)}`);
    }
  }
  return {diffs, files, bytes, entries: names.length};
}

function firstDifference(x, y) {
  if (x.includes(0) || y.includes(0)) return '（二进制）';
  const a = x.toString('utf8').split('\n');
  const b = y.toString('utf8').split('\n');
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    if (a[i] !== b[i]) return `\n    第 ${i + 1} 行\n    Node: ${JSON.stringify(a[i])?.slice(0, 200)}\n    Go:   ${JSON.stringify(b[i])?.slice(0, 200)}`;
  }
  return '';
}

export function run(file, args, {cwd, env, timeout = 300000} = {}) {
  const result = spawnSync(file, args, {cwd, env, encoding: 'utf8', timeout, maxBuffer: 64 * 1024 * 1024});
  return {status: result.status ?? (result.error ? -1 : 0), stdout: result.stdout ?? '', stderr: result.stderr ?? '', error: result.error};
}

export function git(cwd, args) {
  const result = spawnSync('git', args, {cwd, encoding: 'utf8'});
  if (result.status !== 0) throw new ParityFailure(`git ${args.join(' ')} 失败: ${result.stderr}`);
  return result.stdout.trim();
}
