// 当前 Spec/Backend 实例各走一遍 init → 查询类命令 → sync → 回退。
import fs from 'node:fs';
import path from 'node:path';
import {check} from './lib.mjs';

export const PROFILES = ['spec', 'backend'];

export function runProfile(session, profile, tempRoot) {
  session.log(`[profile:${profile}]`);
  const root = path.join(tempRoot, `project-${profile}`);
  const plans = path.join(tempRoot, `plans-${profile}`);
  fs.mkdirSync(plans, {recursive: true});
  const name = ['--project-name', 'Smoke'];

  const initPlan = path.join(plans, 'init.json');
  session.expectOk(['init', '--profile', profile, '--root', root, ...name, '--plan', '--out', initPlan, '--json'], {label: 'init --plan'});
  check(fs.existsSync(initPlan), `init --plan 未写出计划文件: ${initPlan}`);
  check(!fs.existsSync(root), 'init --plan 不应创建项目目录');
  const applied = session.expectOk(['init', '--profile', profile, '--root', root, '--apply', '--plan-file', initPlan, '--json'], {label: 'init --apply'});
  check(applied.result?.status === 'applied', `init --apply 结果应为 applied，实际 ${applied.result?.status}`);
  check(fs.existsSync(path.join(root, 'yss-project.yaml')) || fs.existsSync(path.join(root, '.yss')), '项目实例缺少身份文件或事务目录');

  const json = '--json';
  session.expectOk(['doctor', '--root', root, json], {label: 'doctor'});
  session.expectOk(['diff', '--root', root, json], {label: 'diff'});
  session.expectOk(['context', 'verify', '--root', root, json], {label: 'context verify'});
  const skills = session.expectOk(['skills', 'list', '--root', root, json], {label: 'skills list'});
  check(Array.isArray(skills.result) && skills.result.length > 0, 'skills list 应返回非空标识列表');
  // design / backend 没有阶段资源，result 为 null；只有 spec 返回登记表。
  const assets = session.expectOk(['assets', 'list', '--root', root, json], {label: 'assets list'});
  if (profile === 'spec') check(assets.result && Object.keys(assets.result).length > 0, 'spec 的 assets list 应返回非空登记表');
  const recover = session.expectOk(['recover', '--root', root, json], {label: 'recover'});
  check(recover.result?.transactions?.some(item => item.kind === 'init' && item.phase === 'committed'), 'recover 应列出已提交的 init 事务');
  session.expectOk(['rollback', '--root', root, json], {label: 'rollback（只查询）'});

  const syncPlan = path.join(plans, 'sync.json');
  session.expectOk(['sync', '--root', root, '--plan', '--out', syncPlan, json], {label: 'sync --plan'});
  const synced = session.expectOk(['sync', '--root', root, '--apply', '--plan-file', syncPlan, json], {label: 'sync --apply'});
  check(synced.result?.status === 'unchanged', `刚初始化的实例 sync 应为 unchanged，实际 ${synced.result?.status}`);

  // 回退最近一次成功事务（init），然后确认项目身份已被移除。
  const rolledBack = session.expectOk(['rollback', '--root', root, '--apply', json], {label: 'rollback --apply'});
  check(rolledBack.result?.status === 'rolled-back', `rollback --apply 结果应为 rolled-back，实际 ${rolledBack.result?.status}`);
  session.expectRejected(['doctor', '--root', root, json], {code: 'IDENTITY', label: '回退后 doctor 找不到项目身份'});
}
