import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { applyPlan, parseArgs, planRefresh, regenerationSteps, runRegeneration } from "./refresh-source-lock.mjs";

const TOOL = new URL("./refresh-source-lock.mjs", import.meta.url).pathname;
const POLICY = "tools/policy.json";
const POLICY_BYTES = '{"manifest":{}}\n';

function git(cwd, ...args) {
  const result = spawnSync("git", ["-c", "user.name=t", "-c", "user.email=t@example.com", ...args], { cwd, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  return result.stdout.trim();
}

function tempDir(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "yss-refresh-lock-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  return directory;
}

function commit(repo, name, content = String(Math.random())) {
  fs.writeFileSync(path.join(repo, name), content);
  git(repo, "add", "-A");
  git(repo, "commit", "-q", "-m", name);
  return git(repo, "rev-parse", "HEAD");
}

/** 一个模板仓库：origin 是裸仓库，first 已推送；返回仓库、origin 与第一个提交。 */
function template(t) {
  const base = tempDir(t);
  const origin = path.join(base, "origin.git");
  const repo = path.join(base, "template");
  git(base, "init", "-q", "--bare", origin);
  fs.mkdirSync(repo);
  git(repo, "init", "-q", "-b", "main");
  fs.mkdirSync(path.join(repo, "tools"));
  fs.writeFileSync(path.join(repo, POLICY), POLICY_BYTES);
  const first = commit(repo, "a.txt");
  git(repo, "remote", "add", "origin", origin);
  git(repo, "push", "-q", "origin", "main");
  return { base, repo, first };
}

function lockFor(first, extra = {}) {
  return {
    schemaVersion: 3,
    profiles: {
      spec: { profile: "spec", sourcePath: ".", templateCommit: first, templateVersion: `git:${first}`, policyPath: POLICY, policyHash: "0".repeat(64), legacy: { version: "1" } },
      backend: {
        profile: "backend", sourcePath: "backend", templateCommit: "b".repeat(40), templateVersion: `git:${"b".repeat(40)}`, policyPath: POLICY, policyHash: "1".repeat(64),
        skillsSource: { sourcePath: ".", templateCommit: first, configurationPath: "c.json", configurationHash: "2".repeat(64) },
      },
      ...extra,
    },
  };
}

const sha256 = (text) => createHash("sha256").update(text).digest("hex");

test("预演：目标取模板当前 HEAD，策略哈希按原始字节计算（含末尾换行）", (t) => {
  const { repo, first } = template(t);
  const second = commit(repo, "b.txt");
  git(repo, "push", "-q", "origin", "main");
  const plan = planRefresh({ lock: lockFor(first), sourceRoot: repo, profiles: ["spec"] });
  assert.deepEqual(plan.errors, []);
  assert.equal(plan.entries[0].to, second);
  assert.equal(plan.entries[0].policyTo, sha256(POLICY_BYTES));
  assert.equal(plan.entries[0].changed, true);
});

test("目标已是锁定提交且策略未变：不需要刷新", (t) => {
  const { repo, first } = template(t);
  const lock = lockFor(first);
  lock.profiles.spec.policyHash = sha256(POLICY_BYTES);
  const plan = planRefresh({ lock, sourceRoot: repo, profiles: ["spec"] });
  assert.equal(plan.entries[0].changed, false);
});

test("拒绝：工作区不干净", (t) => {
  const { repo, first } = template(t);
  fs.writeFileSync(path.join(repo, "dirty.txt"), "x");
  const plan = planRefresh({ lock: lockFor(first), sourceRoot: repo, profiles: ["spec"] });
  assert.match(plan.errors[0], /工作区不干净/);
  assert.equal(plan.entries.length, 0);
});

test("拒绝：目标提交尚未推送；显式 allowUnpushed 才放行", (t) => {
  const { repo, first } = template(t);
  commit(repo, "local-only.txt");
  const refused = planRefresh({ lock: lockFor(first), sourceRoot: repo, profiles: ["spec"] });
  assert.match(refused.errors[0], /尚未推送/);
  const allowed = planRefresh({ lock: lockFor(first), sourceRoot: repo, profiles: ["spec"], allowUnpushed: true });
  assert.deepEqual(allowed.errors, []);
});

test("拒绝：目标不是锁定提交的后代（会让锁倒退或跳到别的分支）", (t) => {
  const { repo } = template(t);
  git(repo, "switch", "-q", "-c", "other", "HEAD~0");
  const stray = commit(repo, "other.txt");
  git(repo, "push", "-q", "origin", "other");
  git(repo, "switch", "-q", "main");
  commit(repo, "main2.txt");
  git(repo, "push", "-q", "origin", "main");
  const plan = planRefresh({ lock: lockFor(stray), sourceRoot: repo, profiles: ["spec"] });
  assert.match(plan.errors[0], /不是锁定提交 .* 的后代/);
});

test("拒绝：不存在的模板、没检出的仓库、目标提交里没有策略文件", (t) => {
  const { base, repo, first } = template(t);
  assert.match(planRefresh({ lock: lockFor(first), sourceRoot: repo, profiles: ["design"] }).errors[0], /来源锁里没有这个模板/);
  assert.match(planRefresh({ lock: lockFor(first), sourceRoot: base, profiles: ["backend"] }).errors[0], /不是已检出的 Git 仓库/);
  const lock = lockFor(first);
  lock.profiles.spec.policyPath = "missing.json";
  assert.match(planRefresh({ lock, sourceRoot: repo, profiles: ["spec"] }).errors[0], /里没有 missing\.json/);
});

test("刷新 spec 时提醒 Profile 的 skillsSource 没有跟着变", (t) => {
  const { repo, first } = template(t);
  commit(repo, "b.txt");
  git(repo, "push", "-q", "origin", "main");
  const plan = planRefresh({ lock: lockFor(first), sourceRoot: repo, profiles: ["spec"] });
  assert.ok(plan.warnings.some((warning) => /backend: skillsSource/.test(warning)));
});

test("写锁：只改该模板自己的三个字段，其余字节不变，skillsSource 里相同的旧提交号不被误改", () => {
  const first = "a".repeat(40);
  const next = "c".repeat(40);
  const lock = lockFor(first);
  const text = `${JSON.stringify(lock, null, 2)}\n`;
  const out = applyPlan(text, [{ profile: "spec", from: first, to: next, policyFrom: "0".repeat(64), policyTo: "9".repeat(64), changed: true }]);
  const parsed = JSON.parse(out);
  assert.equal(parsed.profiles.spec.templateCommit, next);
  assert.equal(parsed.profiles.spec.templateVersion, `git:${next}`);
  assert.equal(parsed.profiles.spec.policyHash, "9".repeat(64));
  assert.equal(parsed.profiles.backend.skillsSource.templateCommit, first, "skillsSource 的旧提交号保持不变");
  const expected = JSON.parse(text);
  expected.profiles.spec.templateCommit = next;
  expected.profiles.spec.templateVersion = `git:${next}`;
  expected.profiles.spec.policyHash = "9".repeat(64);
  assert.equal(out, `${JSON.stringify(expected, null, 2)}\n`);
});

test("写锁：旧值与锁文本不符就拒绝，不猜", () => {
  const first = "a".repeat(40);
  const text = `${JSON.stringify(lockFor(first), null, 2)}\n`;
  assert.throws(() => applyPlan(text, [{ profile: "spec", from: "d".repeat(40), to: "c".repeat(40), policyFrom: "0".repeat(64), policyTo: "9".repeat(64), changed: true }]), /旧值与锁文本不符/);
  assert.throws(() => applyPlan(text, [{ profile: "nope", from: first, to: first, policyFrom: "x", policyTo: "y", changed: true }]), /找不到条目/);
  assert.equal(applyPlan(text, [{ profile: "spec", from: first, to: first, policyFrom: "x", policyTo: "y", changed: false }]), text, "未变化的条目不动文本");
});

test("命令行参数：必须绝对路径并明确模板；--regenerate 隐含 --apply；未知参数拒绝", () => {
  assert.throws(() => parseArgs(["--profile", "spec"]), /--source-root 必须是绝对路径/);
  assert.throws(() => parseArgs(["--source-root", "relative", "--profile", "spec"]), /绝对路径/);
  assert.throws(() => parseArgs(["--source-root", "/x"]), /必须用 --profile 明确/);
  assert.throws(() => parseArgs(["--source-root", "/x", "--profile", "bogus"]), /未知模板/);
  assert.throws(() => parseArgs(["--source-root", "/x", "--profile", "spec", "--nope"]), /未知参数/);
  assert.deepEqual(parseArgs(["--source-root", "/x", "--profile", "all"]).profiles, ["spec", "design", "backend", "frontend"]);
  const options = parseArgs(["--source-root", "/x", "--profile", "spec", "--profile", "spec", "--regenerate"]);
  assert.equal(options.apply, true);
  assert.deepEqual(options.profiles, ["spec"]);
});

test("重新生成：按 README 顺序执行，第一步失败就停，测试步骤失败时提示黄金值不自动改写", () => {
  const steps = regenerationSteps("/root");
  assert.deepEqual(steps.map(([, args]) => args.slice(0, 2).join(" ")), [
    "run ./tools/bundle", "run ./tools/helpview", "run ./tools/helpdocs", "run ./tools/helpview", "run ./tools/helpdocs", "test -count=1",
  ]);
  const seen = [];
  const stopEarly = runRegeneration("/root", { log: () => {}, run: (command, args) => { seen.push(args[1]); return seen.length === 2 ? 3 : 0; } });
  assert.equal(stopEarly.ok, false);
  assert.equal(stopEarly.status, 3);
  assert.equal(seen.length, 2, "失败后不再继续");
  const failAtTest = runRegeneration("/root", { log: () => {}, run: (command, args) => (args[0] === "test" ? 1 : 0) });
  assert.match(failAtTest.hint, /黄金值.*不会改写/);
  assert.equal(runRegeneration("/root", { log: () => {}, run: () => 0 }).ok, true);
});

test("命令行端到端：预演不写文件，--apply 才写锁", (t) => {
  const { repo, first } = template(t);
  const second = commit(repo, "b.txt");
  git(repo, "push", "-q", "origin", "main");
  const lockFile = path.join(tempDir(t), "source-lock.json");
  const original = `${JSON.stringify({ schemaVersion: 3, profiles: { spec: lockFor(first).profiles.spec } }, null, 2)}\n`;
  fs.writeFileSync(lockFile, original);
  const dry = spawnSync(process.execPath, [TOOL, "--source-root", repo, "--profile", "spec", "--lock", lockFile], { encoding: "utf8" });
  assert.equal(dry.status, 0, dry.stderr);
  assert.match(dry.stdout, /预演结束，未写任何文件/);
  assert.equal(fs.readFileSync(lockFile, "utf8"), original);
  const applied = spawnSync(process.execPath, [TOOL, "--source-root", repo, "--profile", "spec", "--lock", lockFile, "--apply"], { encoding: "utf8" });
  assert.equal(applied.status, 0, applied.stderr);
  assert.equal(JSON.parse(fs.readFileSync(lockFile, "utf8")).profiles.spec.templateCommit, second);
  const refused = spawnSync(process.execPath, [TOOL, "--source-root", "relative", "--profile", "spec"], { encoding: "utf8" });
  assert.equal(refused.status, 2);
});

// ---- --skills-source：backend / frontend 的技能来源取自 Profile 自己已提交的 profile-skills-source.json ----

const CONFIG_REF = ".template-source/profile-skill-sync.json";
const CONFIG_BYTES = '{"schema_version":1,"profiles":{}}\n';

/** spec 仓库（含技能配置、已推送）加一个嵌在其中的 backend Profile 仓库（也已推送），Profile 的清单指向 spec 的某个提交。 */
function skillsWorkspace(t, { manifestOverrides = {}, pushProfile = true } = {}) {
  const { base, repo: spec, first } = template(t);
  fs.mkdirSync(path.join(spec, ".template-source"), { recursive: true });
  fs.writeFileSync(path.join(spec, CONFIG_REF), CONFIG_BYTES);
  const specConfigCommit = commit(spec, "config.txt");
  git(spec, "push", "-q", "origin", "main");
  fs.appendFileSync(path.join(spec, ".git/info/exclude"), "sub/\n");
  const profileDir = path.join(spec, "sub/backend");
  fs.mkdirSync(path.join(profileDir, "tools"), { recursive: true });
  git(profileDir, "init", "-q", "-b", "main");
  fs.writeFileSync(path.join(profileDir, POLICY), POLICY_BYTES);
  const profileFirst = commit(profileDir, "p1.txt");
  const profileOrigin = path.join(base, "backend-origin.git");
  git(base, "init", "-q", "--bare", profileOrigin);
  git(profileDir, "remote", "add", "origin", profileOrigin);
  const manifest = { schemaVersion: 1, profile: "backend", sourceCommit: specConfigCommit, sourceState: "committed", configurationHash: sha256(CONFIG_BYTES), ...manifestOverrides };
  fs.mkdirSync(path.join(profileDir, ".template-source"), { recursive: true });
  fs.writeFileSync(path.join(profileDir, ".template-source/profile-skills-source.json"), `${JSON.stringify(manifest)}\n`);
  const profileHead = commit(profileDir, "p2.txt");
  if (pushProfile) git(profileDir, "push", "-q", "origin", "main");
  const lock = lockFor(first);
  lock.profiles.spec.policyHash = sha256(POLICY_BYTES);
  lock.profiles.backend = {
    profile: "backend", sourcePath: "sub/backend", templateCommit: profileFirst, templateVersion: `git:${profileFirst}`, policyPath: POLICY, policyHash: "1".repeat(64),
    skillsSource: { sourcePath: ".", templateCommit: first, configurationPath: CONFIG_REF, configurationHash: "2".repeat(64) },
  };
  return { spec, lock, first, specConfigCommit, profileFirst, profileHead };
}

test("--skills-source：skillsSource 取自 Profile 清单记录的提交与配置哈希，并核对配置哈希", (t) => {
  const { spec, lock, first, specConfigCommit } = skillsWorkspace(t);
  const plan = planRefresh({ lock, sourceRoot: spec, profiles: ["backend"], skillsSource: true });
  assert.deepEqual(plan.errors, []);
  const entry = plan.entries[0];
  assert.equal(entry.skills.to.commit, specConfigCommit);
  assert.equal(entry.skills.to.hash, sha256(CONFIG_BYTES));
  assert.equal(entry.skills.from.commit, first);
  assert.equal(entry.skills.changed, true);
  assert.equal(entry.changed, true);
  assert.deepEqual(plan.warnings, [], "取了清单里的值就不再提示 skillsSource 没改");
});

test("不传 --skills-source：skillsSource 保持不动并给出提示", (t) => {
  const { spec, lock } = skillsWorkspace(t);
  const plan = planRefresh({ lock, sourceRoot: spec, profiles: ["backend"] });
  assert.equal(plan.entries[0].skills, undefined);
  assert.ok(plan.warnings.some((warning) => /没有传 --skills-source/.test(warning)));
});

test("--skills-source 拒绝：清单不是 committed、配置哈希不符、来源提交未推送", (t) => {
  const workingTree = skillsWorkspace(t, { manifestOverrides: { sourceState: "working-tree" } });
  assert.match(planRefresh({ lock: workingTree.lock, sourceRoot: workingTree.spec, profiles: ["backend"], skillsSource: true }).errors[0], /sourceState committed/);

  const badHash = skillsWorkspace(t, { manifestOverrides: { configurationHash: "f".repeat(64) } });
  assert.match(planRefresh({ lock: badHash.lock, sourceRoot: badHash.spec, profiles: ["backend"], skillsSource: true }).errors[0], /与 Profile 清单记录的 .* 不符/);

  const unpushed = skillsWorkspace(t);
  const local = commit(unpushed.spec, "unpushed.txt");
  const manifestFile = path.join(unpushed.spec, "sub/backend/.template-source/profile-skills-source.json");
  fs.writeFileSync(manifestFile, `${JSON.stringify({ schemaVersion: 1, profile: "backend", sourceCommit: local, sourceState: "committed", configurationHash: sha256(CONFIG_BYTES) })}\n`);
  commit(path.join(unpushed.spec, "sub/backend"), "p3.txt");
  git(path.join(unpushed.spec, "sub/backend"), "push", "-q", "origin", "main");
  assert.match(planRefresh({ lock: unpushed.lock, sourceRoot: unpushed.spec, profiles: ["backend"], skillsSource: true }).errors[0], /技能来源提交 .* 尚未推送/);
  assert.deepEqual(planRefresh({ lock: unpushed.lock, sourceRoot: unpushed.spec, profiles: ["backend"], skillsSource: true, allowUnpushed: true }).errors, []);
});

test("写锁：只改 skillsSource 的提交与配置哈希，不碰同值的 templateCommit 和别的条目", () => {
  const same = "a".repeat(40);
  const lock = lockFor(same);
  lock.profiles.backend.templateCommit = same;
  lock.profiles.backend.templateVersion = `git:${same}`;
  lock.profiles.backend.skillsSource.templateCommit = same;
  const text = `${JSON.stringify(lock, null, 2)}\n`;
  const entry = {
    profile: "backend", from: same, to: same, policyFrom: lock.profiles.backend.policyHash, policyTo: lock.profiles.backend.policyHash, changed: true,
    skills: { from: { commit: same, hash: "2".repeat(64) }, to: { commit: "c".repeat(40), hash: "9".repeat(64) }, changed: true },
  };
  const out = JSON.parse(applyPlan(text, [entry]));
  assert.equal(out.profiles.backend.skillsSource.templateCommit, "c".repeat(40));
  assert.equal(out.profiles.backend.skillsSource.configurationHash, "9".repeat(64));
  assert.equal(out.profiles.backend.templateCommit, same, "Profile 自己的提交号不受影响");
  assert.equal(out.profiles.spec.templateCommit, same, "spec 条目不受影响");
  assert.throws(() => applyPlan(text, [{ ...entry, skills: { ...entry.skills, from: { commit: "d".repeat(40), hash: "2".repeat(64) } } }]), /旧值与锁文本不符/);
});

test("命令行参数：--skills-source 被识别", () => {
  assert.equal(parseArgs(["--source-root", "/x", "--profile", "backend", "--skills-source"]).skillsSource, true);
  assert.equal(parseArgs(["--source-root", "/x", "--profile", "backend"]).skillsSource, false);
});
