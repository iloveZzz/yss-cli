#!/usr/bin/env node
/** 按真实差异选择验证；未知影响扩大，计划不代表执行通过。 */
import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const ASSET_PACKAGES = ["./internal/bundle", "./tools/bundle", "./tools/package", "./tools/release"];
const HELP_PATTERN = "Help|Presentation|Version";
const sourcePath = value => value.replace(/^tests\/fixtures\/upstream-source\//, "").replace(/^(?:\.codex|\.cursor|\.pi)\/skills\//, ".agents/skills/");

export function selectVerification({ paths = [], sourcePaths = [], versionOnly = false, importGraph = {}, forceFull = false, goChanges = {} }) {
  const packages = new Set(), seeds = new Set(), suites = new Set(), reasons = [], unknown = [];
  let assets = false, help = false, full = forceFull;
  const focusedGo = [];
  for (const file of paths) {
    if (file === "internal/domain/domain.go" && versionOnly) { packages.add("./internal/domain"); help = true; reasons.push("版本常量只改变版本显示；原生入口单独验收"); }
    else if (file === "internal/bundle/assets/bundles.json.gz" || file === "docs/source-lock.json") { assets = true; reasons.push("固定来源和 Bundle：加载器、生产器、封装及原生消费者"); }
    else if (/^internal\/helpview\/assets\/(?:spec|design|backend|frontend)\.json$/.test(file) || file === "docs/cli-reference.md") { help = true; packages.add("./tools/helpview"); packages.add("./tools/helpdocs"); }
    else if (/^internal\/(?:governance\/(?:daily|frontend_local_semantic|backend_local_semantic)(?:_test)?|cli\/daily_test)\.go$/.test(file)) {
      focusedGo.push({ packages: [file.startsWith("internal/cli/") ? "./internal/cli" : "./internal/governance"], run: "Daily|LocalFrontend|BackendLocal|BackendReviewMixed|Frontend.*(?:Local|Verification|Placeholders)" });
      reasons.push(`日常路线与本地前端原始资产消费者: ${file}`);
    }
    else if (file === "internal/governance/spec_baseline.go" && goChanges[file] && !goChanges[file].SharedChanged && goChanges[file].Functions.length > 0 && goChanges[file].Functions.every(name => name === "verifySpecBaselineSource")) {
      focusedGo.push({ packages: ["./internal/governance"], run: "SpecBaseline|LocalFrontend|LifecycleTargetNative.*(?:ProductDesign|SpecDesign)" });
      reasons.push("AST 确认仅批准业务输入消费者变化；导出、接收、前端及生命周期消费者专项");
    }
    else if (file === "internal/governance/specialist_tasks_test.go") focusedGo.push({packages:["./internal/governance"],run:"Specialist|LocalFrontend"});
    else if (file === "internal/governance/task_semantic.go" && goChanges[file] && !goChanges[file].SharedChanged && goChanges[file].Functions.length > 0 && goChanges[file].Functions.every(name => ["enforceHarnessTaskSemanticMode","taskFrontendDeliverySemantic","validateTaskContractSemantic"].includes(name))) {
      focusedGo.push({packages:["./internal/governance"],run:"Task|Approval|Review|Frontend|Local|Backend|ReadOnly|Intake"});
      reasons.push("AST 确认专职任务范围与前端入口：任务、批准、审查及本地/外部消费路径专项");
    }
    else if (/^tools\/verification-scope\/[^/]+\.go$/.test(file)) packages.add("./tools/verification-scope");
    else if (/^(?:go\.(?:mod|sum)|(?:internal\/(?:transaction|updater|project|governance|bundle|domain)|tools\/(?:bundle|package|release))\/.*\.go)$/.test(file)) { full = true; reasons.push(`共享核心或事务/安装/安全边界: ${file}`); }
    else if (/^(?:internal|cmd|tools)\/.*\.go$/.test(file)) seeds.add("./" + path.posix.dirname(file));
    else if (/^(?:README\.md|AGENTS\.md|docs\/[^/]+\.md|compat\/README\.md|\.github\/workflows\/native\.yml|tools\/verification-plan(?:\.test)?\.mjs)$/.test(file)) reasons.push(`文档或验证策略自检: ${file}`);
    else unknown.push(file);
  }
  for (const original of sourcePaths) {
    if (/^tests\/fixtures\/upstream-source(?:\/|-index\.mjs$)/.test(original)) { reasons.push(`冻结测试输入由来源 fixture 检查和原生消费者核验: ${original}`); continue; }
    const file = sourcePath(original);
    if (/^\.agents\/skills\/yss-(?:ddd|layered-mvc)-scaffold-generator\//.test(file) || /^scripts\/(?:lib\/(?:standalone-backend-scaffold|backend-scaffold-prerequisites|scaffold-local-database)\.mjs|fixtures\/backend-scaffold\/)/.test(file)) suites.add("backend-scaffolds");
    else if (/^(?:scripts\/(?:lib\/backend-platform[^/]*\.mjs|backend-platforms)|tests\/backend-platforms\.test\.mjs|\.template-spec\/engineering\/(?:backend-platforms\.|evidence\/)|\.template-source\/engineering\/evidence\/)/.test(file)) { suites.add("backend-platforms"); suites.add("backend-scaffolds"); }
    else if (/^(?:\.agents\/skills\/(?:yss-product-lifecycle|yss-strategic-design|harness-orchestrator|architecture-agent|yss-stage-decision)\/|scripts\/lib\/(?:harness-execution-scope|lifecycle-progression|lifecycle-execution-scope|backend-delivery-terminal|frontend-delivery-boundary|slice-task-package|task-package|spec-baseline)\.mjs|tests\/specialist-business-inputs\.test\.mjs)/.test(file) || /^(?:\.template-spec\/(?:agents\/(?:digital-human-roles|yss-skill-registry)\.yaml|process\/(?:lifecycle-[^/]+\.(?:yaml|json|md)|harness-profile\.yaml|implementation-repo-integration\.md|frontend-backend-delivery\.md|schemas\/slice-implementation-contract-v3\.schema\.json))|scripts\/lib\/digital-human-roles\.mjs)$/.test(file)) suites.add("specialist-business-inputs");
    else if (/^(?:AGENTS\.md|README\.md|scripts\/sync-strategic-handoff-tools|scripts\/lib\/business-tickets\.mjs)$/.test(file)) { suites.add("specialist-business-inputs"); reasons.push("入口与共享分发由 Profile 投影/锁/来源检查核验"); }
    else if (/^\.agents\/skills\/[^/]+\/.*\.md$/.test(file) || /^\.template-source\/profile-skill-patches\//.test(file) || file === ".template-spec/process/harness-process-tailoring.md") { suites.add("specialist-business-inputs"); reasons.push(`技能路径说明及适配由政策、投影和锁核验: ${original}`); }
    else if (/^\.agents\/skills\/[^/]+$/.test(file)||file===".template-source/derived/harness-work-unit-map.md") reasons.push(`派生投影与工作单元映射由来源/投影检查核验: ${original}`);
    else if (file === "tests/efficiency-distribution.test.mjs") suites.add("distribution");
    else if (/^(?:\.agents\/skills\/\.[^/]+\.json|skills-lock\.json|\.template-source\/(?:distribution\/|profile-skill-sync\.json|process\/template-verification[^/]*\.(?:yaml|json))|submodules\/|tests\/fixtures\/specialist-source-fixtures\.py)/.test(file)) reasons.push(`派生来源由投影/锁和原生消费者门禁核验: ${original}`);
    else if (/^scripts\/fixtures\/(?:spec-baseline|strategic-handoff|backend-delivery)\/[^/]+\.mjs$/.test(file)) suites.add("specialist-business-inputs");
    else unknown.push("template:" + original);
  }
  if (seeds.size) {
    const closure = new Set(seeds);
    if ([...seeds].some(seed => !Object.hasOwn(importGraph, seed))) unknown.push("Go 包或依赖图无法证明范围");
    else {
      let changed = true;
      while (changed) {
        changed = false;
        for (const [pkg, imports] of Object.entries(importGraph)) if (!closure.has(pkg) && imports.some(item => closure.has(item))) { closure.add(pkg); changed = true; }
      }
      closure.forEach(pkg => packages.add(pkg));
      reasons.push("Go 实现变化：改变的包及反向导入闭包");
    }
  }
  if (unknown.length) { full = true; reasons.push("无法证明影响范围，扩大为全量: " + unknown.join(", ")); }
  if (forceFull) reasons.push("用户明确要求全量");
  if (assets) { ASSET_PACKAGES.forEach(pkg => packages.add(pkg)); suites.add("distribution"); }
  if (full) return { scope: "full", go: [{ packages: ["./..."] }], race: [{ packages: ["./..."] }], vet: ["./..."], sourceSuites: [...suites].sort(), nativeRequired: true, reasons, unknown };
  const go = packages.size ? [{ packages: [...packages].sort() }] : [];
  for (const group of focusedGo) {
    const existing=go.find(item=>item.run && JSON.stringify(item.packages)===JSON.stringify(group.packages));
    if(existing) existing.run=existing.run===group.run?existing.run:`(?:${existing.run})|(?:${group.run})`;
    else go.push(group);
  }
  if (help) go.push({ packages: ["./internal/cli"], run: HELP_PATTERN });
  return { scope: "impacted", go, race: [...(seeds.size ? [{packages: [...packages].sort()}] : assets ? [{packages: ["./internal/bundle", "./tools/package", "./tools/release"]}] : []), ...go.filter(group => group.run && group.packages.includes("./internal/governance"))], vet: [...new Set(go.flatMap(group => group.packages))].sort(), sourceSuites: [...suites].sort(), nativeRequired: assets || help || focusedGo.length > 0 || seeds.size > 0 || suites.size > 0, reasons, unknown };
}

function git(args, cwd) { return execFileSync("git", args, { cwd, encoding: "utf8", env: { ...process.env, GIT_OPTIONAL_LOCKS: "0" } }).trim(); }
function lines(value) { return value.split("\n").filter(Boolean); }
function sourceDiff(root, before, after) {
  if (!/^[a-f0-9]{40}$/.test(before ?? "") || !/^[a-f0-9]{40}$/.test(after ?? "")) throw new Error("缺少完整固定来源 SHA");
  if (before === after) return [];
  for (const sha of [before, after]) {
    try { git(["cat-file", "-e", sha + "^{commit}"], root); }
    catch { git(["fetch", "--depth=1", "origin", sha], root); }
  }
  return lines(git(["diff", "--name-only", before, after], root));
}

function main(argv) {
  const options = {};
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    if (["--full", "--execute"].includes(flag)) options[flag.slice(2)] = true;
    else if (["--base", "--template-root", "--out"].includes(flag) && argv[i + 1]) options[flag.slice(2)] = argv[++i];
    else throw new Error(`不支持或缺值的参数: ${flag}`);
  }
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const base = options.base || git(["rev-parse", "HEAD^"], root);
  if (!/^[a-f0-9]{40}$/.test(base)) throw new Error("--base 必须是完整提交 SHA");
  const paths = [...new Set([...lines(git(["diff", "--name-only", base], root)), ...lines(git(["ls-files", "--others", "--exclude-standard"], root))])].sort();
  const currentLock = JSON.parse(fs.readFileSync(path.join(root, "docs/source-lock.json")));
  const previousLock = JSON.parse(git(["show", base + ":docs/source-lock.json"], root));
  const sourceChanges = {}, sourcePaths = [];
  const sourceChanged = Object.keys(currentLock.profiles).some(role => currentLock.profiles[role].templateCommit !== previousLock.profiles?.[role]?.templateCommit);
  if (sourceChanged && options["template-root"]) {
    const template = path.resolve(options["template-root"]);
    for (const role of Object.keys(currentLock.profiles)) {
      const sourceRoot = role === "spec" ? template : path.join(template, "submodules", `yss-harness-${role}-agent`);
      sourceChanges[role] = sourceDiff(sourceRoot, previousLock.profiles?.[role]?.templateCommit, currentLock.profiles[role].templateCommit);
      sourcePaths.push(...sourceChanges[role]);
    }
  }
  let versionOnly = false;
  if (paths.includes("internal/domain/domain.go")) {
    const normalize = text => text.replace(/Version\s*=\s*"[^"]+"/, 'Version = "VERSION"');
    versionOnly = normalize(git(["show", base + ":internal/domain/domain.go"], root)) === normalize(fs.readFileSync(path.join(root, "internal/domain/domain.go"), "utf8").trim());
  }
  const module = fs.readFileSync(path.join(root, "go.mod"), "utf8").match(/^module\s+(\S+)/m)[1];
  const importGraph = {};
  for (const line of lines(execFileSync("go", ["list", "-f", '{{.ImportPath}} {{join .Imports " "}}', "./..."], { cwd: root, encoding: "utf8" }))) {
    const [pkg, ...imports] = line.split(" ");
    const local = item => item === module ? "." : "./" + item.slice(module.length + 1);
    importGraph[local(pkg)] = imports.filter(item => item === module || item.startsWith(module + "/")).map(local);
  }
  const scopedFiles=["internal/governance/spec_baseline.go","internal/governance/task_semantic.go"].filter(file=>paths.includes(file));
  const goChanges=scopedFiles.length?JSON.parse(execFileSync("go",["run","./tools/verification-scope"],{cwd:root,encoding:"utf8",input:JSON.stringify(Object.fromEntries(scopedFiles.map(file=>[file,{Before:git(["show",base+":"+file],root),After:fs.readFileSync(path.join(root,file),"utf8")}])))})):{};
  const plan = { schemaVersion: 1, baseCommit: base, cliCommit: git(["rev-parse", "HEAD"], root), sourceLockSha256: createHash("sha256").update(fs.readFileSync(path.join(root, "docs/source-lock.json"))).digest("hex"), paths, sourceChanges, sourceAnalysisPending: sourceChanged && !options["template-root"], ...selectVerification({ paths, sourcePaths, versionOnly, importGraph, forceFull: options.full, goChanges }) };
  if (plan.sourceAnalysisPending) plan.nativeRequired = true;
  if (options.out) fs.writeFileSync(options.out, JSON.stringify(plan, null, 2) + "\n");
  if (process.env.GITHUB_OUTPUT) fs.appendFileSync(process.env.GITHUB_OUTPUT, `native_required=${plan.nativeRequired}\nsource_changed=${sourceChanged}\nscope=${plan.scope}\n`);
  process.stdout.write(JSON.stringify(plan, null, 2) + "\n");
  if (!options.execute) return;
  if (plan.sourceAnalysisPending) throw new Error("来源变化尚未分析；先提供 --template-root，不能执行未闭合的验证计划");
  function run(command, args, cwd = root, env = process.env) {
    const result = spawnSync(command, args, { cwd, env, stdio: "inherit" });
    if (result.error) throw result.error;
    if (result.status !== 0) process.exit(result.status ?? 1);
  }
  run(process.execPath, ["--test", "tools/verification-plan.test.mjs"]);
  for (const group of plan.go) run("go", ["test", "-p", "1", "-count=1", "-timeout=60m", ...(group.run ? ["-run", group.run] : []), ...group.packages]);
  if (plan.vet.length) run("go", ["vet", ...plan.vet]);
  run("go", ["mod", "verify"]);
  for (const group of plan.race) run("go", ["test", "-p", "1", "-race", "-count=1", "-timeout=120m", ...(group.run ? ["-run",group.run] : []), ...group.packages], root, { ...process.env, CGO_ENABLED: "1" });
  if (plan.sourceSuites.length && !options["template-root"]) throw new Error("专项来源测试缺少 --template-root");
  const template = options["template-root"] && path.resolve(options["template-root"]);
  const tests = new Set();
  for (const suite of plan.sourceSuites) {
    if (suite === "specialist-business-inputs") tests.add("tests/specialist-business-inputs.test.mjs");
    if (suite === "distribution") tests.add("tests/efficiency-distribution.test.mjs");
    if (suite === "backend-platforms") tests.add("tests/backend-platforms.test.mjs");
    if (suite === "backend-scaffolds") for (const ref of ["yss-ddd-scaffold-generator/scripts/scaffold-generator.test.mjs", "yss-ddd-scaffold-generator/scripts/first-slice-compatibility.test.mjs", "yss-ddd-scaffold-generator/scripts/standalone.test.mjs", "yss-layered-mvc-scaffold-generator/scripts/scaffold-generator.test.mjs"]) {
      const file = ".agents/skills/" + ref;
      if (fs.existsSync(path.join(template, file))) tests.add(file);
      else if (!ref.endsWith("standalone.test.mjs")) throw new Error("来源专项测试缺失: " + file);
    }
  }
  if (tests.size) run(process.execPath, ["--test", "--test-concurrency=1", ...tests], template);
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { main(process.argv.slice(2)); } catch (error) { process.stderr.write(error.message + "\n"); process.exitCode = 1; }
}
