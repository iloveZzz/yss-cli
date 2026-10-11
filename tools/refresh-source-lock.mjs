#!/usr/bin/env node
// 把 docs/source-lock.json 里指定模板的固定提交刷新到该模板当前 HEAD，并按 README 的顺序重新生成 Bundle。
// 默认只预演；--apply 才写锁；--regenerate 在写锁后依次运行生成与校验命令。
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const PROFILES = ["spec", "design", "backend", "frontend"];
const CLI_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function git(repo, args) {
  const result = spawnSync("git", ["-C", repo, ...args], { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  return { status: result.status, stdout: (result.stdout ?? "").trim(), stderr: (result.stderr ?? "").trim() };
}

/** 逐个模板检查并算出新的固定值；不写任何文件。errors 非空时不得应用。 */
export function planRefresh({ lock, sourceRoot, profiles, allowUnpushed = false, skillsSource = false }) {
  const entries = [];
  const errors = [];
  const warnings = [];
  for (const profile of profiles) {
    const source = lock.profiles?.[profile];
    if (!source) { errors.push(`${profile}: 来源锁里没有这个模板`); continue; }
    const repo = path.resolve(sourceRoot, source.sourcePath);
    if (!existsSync(path.join(repo, ".git"))) { errors.push(`${profile}: ${source.sourcePath} 不是已检出的 Git 仓库`); continue; }
    const status = git(repo, ["status", "--porcelain", "--untracked-files=all"]);
    if (status.status !== 0) { errors.push(`${profile}: 无法读取工作区状态（${status.stderr}）`); continue; }
    if (status.stdout) { errors.push(`${profile}: 工作区不干净；Bundle 要求来源是 committed，先提交或还原`); continue; }
    const target = git(repo, ["rev-parse", "HEAD"]).stdout;
    // 策略哈希必须按原始字节算（git 的文本输出会被去掉末尾换行）
    const policy = spawnSync("git", ["-C", repo, "show", `${target}:${source.policyPath}`], { maxBuffer: 64 * 1024 * 1024 });
    if (policy.status !== 0) { errors.push(`${profile}: 目标提交 ${target.slice(0, 8)} 里没有 ${source.policyPath}`); continue; }
    if (target !== source.templateCommit) {
      if (git(repo, ["merge-base", "--is-ancestor", source.templateCommit, target]).status !== 0) {
        errors.push(`${profile}: 目标提交 ${target.slice(0, 8)} 不是锁定提交 ${source.templateCommit.slice(0, 8)} 的后代，刷新会让锁倒退或跳到别的分支`);
        continue;
      }
      const remotes = git(repo, ["branch", "-r", "--contains", target]).stdout;
      if (!remotes && !allowUnpushed) {
        errors.push(`${profile}: 目标提交 ${target.slice(0, 8)} 尚未推送到任何远端分支；把它写进来源锁会让别人拿不到这个提交。先推送，或明确传 --allow-unpushed`);
        continue;
      }
    }
    const policyTo = createHash("sha256").update(policy.stdout).digest("hex");
    entries.push({
      profile,
      from: source.templateCommit,
      to: target,
      policyFrom: source.policyHash,
      policyTo,
      changed: target !== source.templateCommit || policyTo !== source.policyHash,
    });
    const skills = source.skillsSource;
    if (skills && skillsSource && (profile === "backend" || profile === "frontend")) {
      const result = planSkillsSource({ profile, source, repo, target, sourceRoot, allowUnpushed });
      if (result.error) errors.push(result.error);
      else {
        const entry = entries[entries.length - 1];
        entry.skills = result.skills;
        entry.changed = entry.changed || result.skills.changed;
      }
    } else if (skills && skills.templateCommit !== target && profile !== "spec") {
      warnings.push(`${profile}: skillsSource 固定在 ${skills.templateCommit.slice(0, 8)}，没有传 --skills-source，本脚本不改它，需要时单独核对`);
    }
  }
  for (const other of Object.keys(lock.profiles ?? {})) {
    const skills = lock.profiles[other].skillsSource;
    const specEntry = entries.find((entry) => entry.profile === "spec");
    if (!skillsSource && specEntry && skills && skills.templateCommit !== specEntry.to) {
      warnings.push(`${other}: skillsSource 的 Spec 来源 ${skills.templateCommit.slice(0, 8)} 不等于刷新后的 spec ${specEntry.to.slice(0, 8)}，本脚本不改它`);
    }
  }
  return { entries, errors, warnings: [...new Set(warnings)] };
}

/**
 * backend / frontend 的技能由「技能来源」（spec 的某个提交加它的 profile-skill-sync.json）组合而成；打包器要求 Profile 已提交的
 * profile-skills-source.json 与锁里的 skillsSource 提交、配置哈希完全一致。所以取值只能来自 Profile 自己的清单，不能自己挑。
 */
function planSkillsSource({ profile, source, repo, target, sourceRoot, allowUnpushed }) {
  const manifestFile = spawnSync("git", ["-C", repo, "show", `${target}:.template-source/profile-skills-source.json`], { maxBuffer: 16 * 1024 * 1024 });
  if (manifestFile.status !== 0) return { error: `${profile}: 目标提交里没有 .template-source/profile-skills-source.json，无法确定技能来源` };
  let manifest;
  try { manifest = JSON.parse(manifestFile.stdout.toString("utf8")); } catch { return { error: `${profile}: profile-skills-source.json 不是有效 JSON` }; }
  if (manifest.schemaVersion !== 1 || manifest.profile !== profile || manifest.sourceState !== "committed" || !/^[0-9a-f]{40}$/.test(manifest.sourceCommit ?? "")) {
    return { error: `${profile}: profile-skills-source.json 必须是 schemaVersion 1、同名模板、sourceState committed 且带完整 sourceCommit` };
  }
  const specRepo = path.resolve(sourceRoot, source.skillsSource.sourcePath);
  if (git(specRepo, ["cat-file", "-e", `${manifest.sourceCommit}^{commit}`]).status !== 0) {
    return { error: `${profile}: 技能来源提交 ${manifest.sourceCommit.slice(0, 8)} 在 ${source.skillsSource.sourcePath} 里不存在` };
  }
  if (!git(specRepo, ["branch", "-r", "--contains", manifest.sourceCommit]).stdout && !allowUnpushed) {
    return { error: `${profile}: 技能来源提交 ${manifest.sourceCommit.slice(0, 8)} 尚未推送到任何远端分支；先推送，或明确传 --allow-unpushed` };
  }
  const config = spawnSync("git", ["-C", specRepo, "show", `${manifest.sourceCommit}:${source.skillsSource.configurationPath}`], { maxBuffer: 16 * 1024 * 1024 });
  if (config.status !== 0) return { error: `${profile}: 技能来源提交里没有 ${source.skillsSource.configurationPath}` };
  const hash = createHash("sha256").update(config.stdout).digest("hex");
  if (hash !== manifest.configurationHash) return { error: `${profile}: 技能来源提交里配置的哈希 ${hash.slice(0, 8)} 与 Profile 清单记录的 ${String(manifest.configurationHash).slice(0, 8)} 不符` };
  const from = source.skillsSource;
  return { skills: { from: { commit: from.templateCommit, hash: from.configurationHash }, to: { commit: manifest.sourceCommit, hash }, changed: from.templateCommit !== manifest.sourceCommit || from.configurationHash !== hash } };
}

/** 只替换每个模板自己的 templateCommit / templateVersion / policyHash，保留文件其余字节；旧值与解析结果不符就拒绝。 */
export function applyPlan(lockText, entries) {
  let text = lockText;
  for (const entry of entries.filter((item) => item.changed)) {
    const anchor = text.indexOf(`"profile": "${entry.profile}"`);
    if (anchor < 0) throw new Error(`${entry.profile}: 锁文本里找不到条目`);
    const replaceAfter = (index, key, oldValue, newValue) => {
      const needle = `"${key}": "${oldValue}"`;
      const at = text.indexOf(needle, index);
      const nextProfile = text.indexOf('"profile": "', index + 1);
      if (at < 0 || (nextProfile >= 0 && at > nextProfile)) throw new Error(`${entry.profile}: ${key} 的旧值与锁文本不符，拒绝改写`);
      text = text.slice(0, at) + `"${key}": "${newValue}"` + text.slice(at + needle.length);
      return at;
    };
    replaceAfter(anchor, "templateCommit", entry.from, entry.to);
    replaceAfter(anchor, "templateVersion", `git:${entry.from}`, `git:${entry.to}`);
    replaceAfter(anchor, "policyHash", entry.policyFrom, entry.policyTo);
    if (entry.skills?.changed) {
      const skillsAnchor = text.indexOf('"skillsSource"', anchor);
      const nextProfile = text.indexOf('"profile": "', anchor + 1);
      if (skillsAnchor < 0 || (nextProfile >= 0 && skillsAnchor > nextProfile)) throw new Error(`${entry.profile}: 锁文本里找不到 skillsSource，拒绝改写`);
      replaceAfter(skillsAnchor, "templateCommit", entry.skills.from.commit, entry.skills.to.commit);
      replaceAfter(skillsAnchor, "configurationHash", entry.skills.from.hash, entry.skills.to.hash);
    }
  }
  return text;
}

/** 写锁后的生成与校验命令，顺序取自 README；任何一步失败就停。 */
export function regenerationSteps(sourceRoot) {
  return [
    ["go", ["run", "./tools/bundle", "--source-root", sourceRoot, "--lock", "docs/source-lock.json", "--out", "internal/bundle/assets"]],
    ["go", ["run", "./tools/helpview"]],
    ["go", ["run", "./tools/helpdocs", "--reference"]],
    ["go", ["run", "./tools/helpview", "--check"]],
    ["go", ["run", "./tools/helpdocs", "--reference", "--check"]],
    ["go", ["test", "-count=1", "./internal/bundle"]],
  ];
}

export function runRegeneration(sourceRoot, { run = defaultRun, log = (line) => process.stdout.write(`${line}\n`) } = {}) {
  for (const [command, args] of regenerationSteps(sourceRoot)) {
    log(`$ ${command} ${args.join(" ")}`);
    const status = run(command, args);
    if (status !== 0) {
      const hint = args[0] === "test"
        ? "若失败在 internal/bundle/skill_lock_test.go 的 product-design 渲染黄金值：它是对 Node treeHash（scripts/lib/skill-supply-chain.mjs）与模板 skills-lock.json 的独立三方核对，需人工核对后再改，脚本不会改写。"
        : "修复上面的错误后，从这一步重新运行。";
      return { ok: false, failed: [command, ...args].join(" "), status, hint };
    }
  }
  return { ok: true };
}

function defaultRun(command, args) {
  return spawnSync(command, args, { cwd: CLI_ROOT, stdio: "inherit" }).status ?? 1;
}

function usage() {
  return [
    "用法: node tools/refresh-source-lock.mjs --source-root <四模板根的绝对路径> --profile <spec|design|backend|frontend|all> [--profile ...]",
    "                                         [--lock docs/source-lock.json] [--apply] [--regenerate] [--allow-unpushed] [--skills-source]",
    "默认只预演。--apply 写锁；--regenerate 写锁后重新生成 Bundle、helpview、cli-help 并校验。",
    "目标提交必须已推送（本地远端跟踪分支包含它），否则拒绝；--allow-unpushed 仅用于本机试验。",
    "--skills-source：同时把 backend / frontend 的 skillsSource 刷新为各自 profile-skills-source.json 记录的提交与配置哈希（需已推送）。",
  ].join("\n");
}

export function parseArgs(argv) {
  const options = { profiles: [], lock: "docs/source-lock.json", apply: false, regenerate: false, allowUnpushed: false, skillsSource: false, sourceRoot: null };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--source-root") options.sourceRoot = argv[++index];
    else if (arg === "--profile") {
      const value = argv[++index];
      if (value === "all") options.profiles.push(...PROFILES);
      else if (PROFILES.includes(value)) options.profiles.push(value);
      else throw new Error(`未知模板 ${value}`);
    } else if (arg === "--lock") options.lock = argv[++index];
    else if (arg === "--apply") options.apply = true;
    else if (arg === "--regenerate") { options.regenerate = true; options.apply = true; }
    else if (arg === "--allow-unpushed") options.allowUnpushed = true;
    else if (arg === "--skills-source") options.skillsSource = true;
    else if (arg === "--help") options.help = true;
    else throw new Error(`未知参数 ${arg}`);
  }
  if (options.help) return options;
  if (!options.sourceRoot || !path.isAbsolute(options.sourceRoot)) throw new Error("--source-root 必须是绝对路径");
  if (!options.profiles.length) throw new Error("必须用 --profile 明确要刷新的模板（避免误动 Profile 条目）");
  options.profiles = [...new Set(options.profiles)];
  return options;
}

function main(argv) {
  const options = parseArgs(argv);
  if (options.help) { process.stdout.write(`${usage()}\n`); return 0; }
  const lockPath = path.resolve(CLI_ROOT, options.lock);
  const lockText = readFileSync(lockPath, "utf8");
  const plan = planRefresh({ lock: JSON.parse(lockText), sourceRoot: options.sourceRoot, profiles: options.profiles, allowUnpushed: options.allowUnpushed, skillsSource: options.skillsSource });
  for (const entry of plan.entries) {
    process.stdout.write(entry.changed
      ? `${entry.profile}: ${entry.from.slice(0, 8)} → ${entry.to.slice(0, 8)}，policyHash ${entry.policyFrom.slice(0, 8)} → ${entry.policyTo.slice(0, 8)}\n`
      : `${entry.profile}: 已是 ${entry.to.slice(0, 8)}，无需刷新\n`);
    if (entry.skills?.changed) process.stdout.write(`${entry.profile}: skillsSource ${entry.skills.from.commit.slice(0, 8)} → ${entry.skills.to.commit.slice(0, 8)}，configurationHash ${entry.skills.from.hash.slice(0, 8)} → ${entry.skills.to.hash.slice(0, 8)}\n`);
  }
  for (const warning of plan.warnings) process.stdout.write(`注意 ${warning}\n`);
  if (plan.errors.length) { process.stderr.write(`${plan.errors.join("\n")}\n拒绝刷新。\n`); return 1; }
  if (!options.apply) { process.stdout.write("预演结束，未写任何文件。加 --apply 写锁，加 --regenerate 同时重新生成 Bundle。\n"); return 0; }
  const next = applyPlan(lockText, plan.entries);
  if (next !== lockText) writeFileSync(lockPath, next);
  process.stdout.write(next === lockText ? "锁无变化\n" : `已写入 ${path.relative(CLI_ROOT, lockPath)}\n`);
  if (!options.regenerate) {
    process.stdout.write("下一步（或重跑并加 --regenerate）:\n");
    for (const [command, args] of regenerationSteps(options.sourceRoot)) process.stdout.write(`  ${command} ${args.join(" ")}\n`);
    return 0;
  }
  const result = runRegeneration(options.sourceRoot);
  if (!result.ok) { process.stderr.write(`失败于: ${result.failed}（退出码 ${result.status}）\n${result.hint}\n`); return 1; }
  process.stdout.write("来源锁与生成物已刷新。提交 yss-cli 后，再更新模板仓的 submodules/yss-cli 指针。\n");
  return 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { process.exitCode = main(process.argv.slice(2)); } catch (error) { process.stderr.write(`refresh-source-lock 错误: ${error.message}\n${usage()}\n`); process.exitCode = 2; }
}
