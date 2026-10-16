// yss git 写入路径：临时仓库 + 本地裸远端（file://）上的 计划→应用 主流程与闸门。
import fs from 'node:fs';
import path from 'node:path';
import {check, fail} from './lib.mjs';

const TICKET = 'FEAT-1';
const BRANCH = `feat/${TICKET}-smoke`;

function git(session, cwd, args) {
  const result = session.git(args, {cwd});
  check(result.status === 0, `git ${args.join(' ')} 失败 (${result.status}): ${result.stderr.trim()}`);
  return result.stdout.trim();
}

export function runGit(session, tempRoot) {
  session.log('[git]');
  const remoteDir = path.join(tempRoot, 'remote.git');
  const repo = path.join(tempRoot, 'repo');
  const plans = path.join(tempRoot, 'plans-git'); // 计划文件须在仓库之外
  fs.mkdirSync(plans, {recursive: true});
  fs.mkdirSync(repo, {recursive: true});

  git(session, tempRoot, ['init', '--bare', '-q', remoteDir]);
  git(session, repo, ['init', '-q']);
  fs.writeFileSync(path.join(repo, 'README.md'), 'smoke\n');
  git(session, repo, ['add', 'README.md']);
  git(session, repo, ['commit', '-q', '-m', 'init']);
  // yss 事务目录 .yss/ 不应计入工作区状态（真实仓库通常在 .gitignore 中忽略），否则 release tag 要求的干净 HEAD 永远不满足。
  fs.appendFileSync(path.join(repo, '.git', 'info', 'exclude'), '.yss/\n');
  git(session, repo, ['remote', 'add', 'origin', `file://${remoteDir}`]);
  git(session, repo, ['push', '-q', 'origin', 'main']);
  const base = git(session, repo, ['rev-parse', 'HEAD']);
  const remoteRef = ref => session.git(['--git-dir', remoteDir, 'rev-parse', '--verify', '-q', ref]);

  // 写入类子命令统一走 保存计划 → 应用 两步。
  const planned = (name, subcommand, planArgs, applyArgs = []) => {
    const planFile = path.join(plans, `${name}.json`);
    session.expectOk(['git', ...subcommand, '--root', repo, ...planArgs, '--plan', '--out', planFile, '--json'], {label: `${name} --plan`});
    check(fs.existsSync(planFile), `${name} 未写出计划文件`);
    return session.expectOk(['git', ...subcommand, '--root', repo, '--apply', '--plan-file', planFile, ...applyArgs, '--json'], {label: `${name} --apply`});
  };

  // 只读查询
  const status = session.expectOk(['git', 'status', '--root', repo, '--json'], {label: 'status'});
  check(status.result?.clean === true && status.result?.branch?.head === 'main', 'status 应显示 main 且工作区干净');
  session.expectOk(['git', 'doctor', '--root', repo, '--json'], {statuses: ['ok', 'warn'], label: 'doctor'});
  const baseResult = session.expectOk(['git', 'base', '--root', repo, '--json'], {label: 'base'});
  check(JSON.stringify(baseResult.result).includes(base), 'base 应解析出完整 SHA');
  const policy = session.expectOk(['git', 'policy', '--root', repo, '--json'], {label: 'policy'});
  check(policy.result?.source === 'default', '尚未写入策略文件时 policy 应来自默认值');

  // 在受保护的 main 上直接提交应被拒绝（拒绝发生在计划阶段）
  fs.writeFileSync(path.join(repo, 'blocked.txt'), 'x\n');
  session.expectRejected(['git', 'commit', '--root', repo, '--task', TICKET, '--message', '不应成功', '--paths', 'blocked.txt', '--plan', '--out', path.join(plans, 'blocked.json'), '--json'], {label: '受保护分支 commit --plan'});
  fs.rmSync(path.join(repo, 'blocked.txt'));

  // branch start
  planned('branch-start', ['branch', 'start'], ['--task', TICKET, '--slug', 'smoke']);
  check(git(session, repo, ['rev-parse', '--abbrev-ref', 'HEAD']) === BRANCH, `branch start 后应位于 ${BRANCH}`);

  // hooks install（钩子靠 PATH 里的 yss 校验提交信息）、policy set
  planned('hooks-install', ['hooks', 'install'], []);
  check(fs.existsSync(path.join(repo, '.githooks', 'commit-msg')), 'hooks install 应写入 .githooks/commit-msg');
  check(git(session, repo, ['config', '--local', 'core.hooksPath']).length > 0, 'hooks install 应设置本地 core.hooksPath');
  planned('policy-set', ['policy', 'set'], ['--key', 'protected_branches', '--value', '["main","release"]']);
  const policyAfter = session.expectOk(['git', 'policy', '--root', repo, '--json'], {label: 'policy（已写入）'});
  check(policyAfter.result?.protectedBranches?.includes('release'), 'policy set 之后 protectedBranches 应包含 release');

  // commit：只提交列出的路径，并追加 Ticket trailer；未列出的文件保持未提交
  fs.writeFileSync(path.join(repo, 'feature.txt'), 'feature\n');
  fs.writeFileSync(path.join(repo, 'other.txt'), 'not listed\n');
  planned('commit', ['commit'], ['--task', TICKET, '--message', '冒烟提交', '--paths', 'feature.txt,.githooks,.template-spec']);
  const message = git(session, repo, ['log', '-1', '--format=%B']);
  check(message.includes('冒烟提交') && message.includes(`Ticket: ${TICKET}`), `提交信息应含主题与 Ticket trailer，实际:\n${message}`);
  const committed = git(session, repo, ['show', '--name-only', '--format=', 'HEAD']).split('\n');
  check(committed.includes('feature.txt') && !committed.includes('other.txt'), `提交应只含列出的路径，实际: ${committed.join(',')}`);
  check(git(session, repo, ['status', '--porcelain', '--', 'other.txt']).startsWith('??'), 'other.txt 应保持未跟踪');

  // 已安装的钩子拒绝缺少 Ticket trailer 的普通 git 提交
  fs.writeFileSync(path.join(repo, 'plain.txt'), 'plain\n');
  git(session, repo, ['add', 'plain.txt']);
  const plain = session.git(['commit', '-q', '-m', '缺少 trailer'], {cwd: repo});
  check(plain.status !== 0, '钩子应拒绝没有 Ticket trailer 的提交');
  git(session, repo, ['reset', '-q']);
  fs.rmSync(path.join(repo, 'plain.txt'));
  fs.rmSync(path.join(repo, 'other.txt'));

  session.expectOk(['git', 'audit', '--root', repo, '--since', base, '--json'], {statuses: ['ok', 'attention'], label: 'audit'});

  // push：--confirm-remote 闸门
  const pushPlan = path.join(plans, 'push-branch.json');
  session.expectOk(['git', 'push', '--root', repo, '--remote', 'origin', '--plan', '--out', pushPlan, '--json'], {label: 'push --plan'});
  check(remoteRef(`refs/heads/${BRANCH}`).status !== 0, 'push --plan 不得触达远端');
  session.expectRejected(['git', 'push', '--root', repo, '--apply', '--plan-file', pushPlan, '--json'], {label: 'push --apply 缺少 --confirm-remote'});
  session.expectRejected(['git', 'push', '--root', repo, '--apply', '--plan-file', pushPlan, '--confirm-remote', 'upstream', '--json'], {label: 'push --apply 远端名不符'});
  check(remoteRef(`refs/heads/${BRANCH}`).status !== 0, '闸门拒绝后远端不得出现分支');
  session.expectOk(['git', 'push', '--root', repo, '--apply', '--plan-file', pushPlan, '--confirm-remote', 'origin', '--json'], {label: 'push --apply --confirm-remote'});
  check(remoteRef(`refs/heads/${BRANCH}`).stdout.trim() === git(session, repo, ['rev-parse', 'HEAD']), '远端分支应指向本地 HEAD');

  // 模拟合并回默认分支后：受保护分支拒绝 push；release tag 与标签 push
  git(session, repo, ['checkout', '-q', 'main']);
  git(session, repo, ['merge', '-q', '--ff-only', BRANCH]);
  session.expectRejected(['git', 'push', '--root', repo, '--remote', 'origin', '--plan', '--out', path.join(plans, 'push-main.json'), '--json'], {label: '受保护分支 push --plan'});
  planned('release-tag', ['release', 'tag'], ['--tag-version', '0.0.1']);
  check(git(session, repo, ['cat-file', '-t', 'refs/tags/v0.0.1']) === 'tag', 'release tag 应创建带注释的 v0.0.1');
  check(remoteRef('refs/tags/v0.0.1').status !== 0, 'release tag 不得推送远端');
  planned('push-tag', ['push'], ['--remote', 'origin', '--tag', 'v0.0.1'], ['--confirm-remote', 'origin']);
  check(remoteRef('refs/tags/v0.0.1').status === 0, '标签 push 之后远端应有 v0.0.1');
}
