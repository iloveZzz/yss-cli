// 构建后黑盒冒烟的公共工具：隔离环境、运行被测二进制、断言 envelope。
// 这不是测试体系：不含 fixture、预言机或验收回执，只检查已构建二进制的主路径能否走通。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {spawnSync} from 'node:child_process';

export class SmokeFailure extends Error {}

export function fail(message) {
  throw new SmokeFailure(message);
}

export function check(condition, message) {
  if (!condition) fail(message);
}

// macOS 的临时目录经符号链接，CLI 的 PATH 校验要求真实路径，所以统一 realpath。
export function makeTempRoot(prefix = 'yss-smoke-') {
  return fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), prefix)));
}

// 被测二进制以 `yss` 名称放进独立目录并排在 PATH 最前，供 git 钩子等子进程按名称找到。
export function installBinary(binary, tempRoot) {
  const source = fs.realpathSync(binary);
  const dir = path.join(tempRoot, 'bin');
  fs.mkdirSync(dir, {recursive: true});
  const name = process.platform === 'win32' ? 'yss.exe' : 'yss';
  fs.copyFileSync(source, path.join(dir, name));
  fs.chmodSync(path.join(dir, name), 0o755);
  return dir;
}

// 隔离的 git 与运行环境：全局配置指向临时文件，系统配置关闭，HOME 也在临时目录内。
// yss 会清除全部 GIT_* 环境变量，只按 HOME 读取全局配置，所以 GIT_CONFIG_GLOBAL 与
// $HOME/.gitconfig 指向同一个临时文件，直接调用的 git 与被测 yss 看到相同的身份配置。
export function isolatedEnv(tempRoot, binDir) {
  const home = path.join(tempRoot, 'home');
  fs.mkdirSync(home, {recursive: true});
  const gitConfig = path.join(home, '.gitconfig');
  fs.writeFileSync(gitConfig, '[user]\n\tname = Smoke Tester\n\temail = smoke@example.invalid\n[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n');
  const env = {
    PATH: [binDir, process.env.PATH ?? ''].join(path.delimiter),
    HOME: home,
    USERPROFILE: home,
    XDG_CONFIG_HOME: path.join(home, '.config'),
    GIT_CONFIG_GLOBAL: gitConfig,
    GIT_CONFIG_NOSYSTEM: '1',
    GIT_TERMINAL_PROMPT: '0',
    LC_ALL: 'C',
  };
  for (const key of ['SystemRoot', 'TMPDIR', 'TEMP', 'TMP']) {
    if (process.env[key]) env[key] = process.env[key];
  }
  return env;
}

function run(file, args, {cwd, env}) {
  const result = spawnSync(file, args, {cwd, env, encoding: 'utf8', maxBuffer: 256 * 1024 * 1024});
  if (result.error) fail(`无法执行 ${file}: ${result.error.message}`);
  return {status: result.status, signal: result.signal, stdout: result.stdout ?? '', stderr: result.stderr ?? ''};
}

export class Session {
  constructor({binary, tempRoot, log = console.log}) {
    this.tempRoot = tempRoot;
    this.binDir = installBinary(binary, tempRoot);
    this.env = isolatedEnv(tempRoot, this.binDir);
    this.log = log;
  }

  // 以 PATH 中的 yss 运行；返回原始结果与解析后的 envelope（非 JSON 时 envelope 为 null）。
  yss(args, {cwd = this.tempRoot} = {}) {
    const result = run('yss', args, {cwd, env: this.env});
    let envelope = null;
    try {envelope = JSON.parse(result.stdout)} catch {}
    return {...result, envelope, command: ['yss', ...args].join(' ')};
  }

  git(args, {cwd = this.tempRoot} = {}) {
    return run('git', args, {cwd, env: this.env});
  }

  // 断言退出码为 0 且 envelope.status=ok、code=OK。附带可选的额外状态集合（如 git audit 的 attention）。
  expectOk(args, {cwd, statuses = ['ok'], label} = {}) {
    const result = this.yss(args, {cwd});
    check(result.status === 0, `${result.command}: 退出码 ${result.status}（期望 0）\n${excerpt(result)}`);
    check(result.envelope, `${result.command}: 标准输出不是 JSON envelope\n${excerpt(result)}`);
    check(statuses.includes(result.envelope.status), `${result.command}: status=${result.envelope.status}（期望 ${statuses.join('|')}）\n${excerpt(result)}`);
    check(result.envelope.code === 'OK', `${result.command}: code=${result.envelope.code}（期望 OK）\n${excerpt(result)}`);
    this.log(`  ok   ${label ?? result.command.replace(/\s--\S+\s\S+/g, '').slice(0, 80)}`);
    return result.envelope;
  }

  // 断言命令被拒绝：非 0 退出码、envelope.status=error，并可校验错误码。
  expectRejected(args, {cwd, code, label} = {}) {
    const result = this.yss(args, {cwd});
    check(result.status !== 0 && result.status !== null, `${result.command}: 期望被拒绝，实际退出码 ${result.status}\n${excerpt(result)}`);
    check(result.envelope && result.envelope.status === 'error', `${result.command}: 被拒绝时应返回 status=error 的 envelope\n${excerpt(result)}`);
    if (code) check(result.envelope.code === code, `${result.command}: code=${result.envelope.code}（期望 ${code}）\n${excerpt(result)}`);
    this.log(`  ok   拒绝：${label ?? result.command.slice(0, 80)}（${result.envelope.code}）`);
    return result.envelope;
  }
}

function excerpt(result) {
  const out = result.stdout.length > 800 ? result.stdout.slice(0, 800) + '…' : result.stdout;
  return `stdout: ${out.trim()}\nstderr: ${result.stderr.trim().slice(0, 400)}`;
}
