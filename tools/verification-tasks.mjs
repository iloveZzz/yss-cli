/** One discovered inventory, shared by local execution and the native CI matrix. */
import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { runCommands } from "./verification-runner.mjs";

export const PACKAGE_TIMEOUT = "30m";
export const SLOW_TIMEOUT = "30m";
export const SLOW_TESTS = [{ package: "./internal/governance", names: ["TestLifecycleTargetNativeSpecDesignBusinessSequence"] }];
const hash = value => createHash("sha256").update(value).digest("hex");
const exact = names => `^(${names.map(name => name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|")})$`;
const readCommand = (command, args, cwd) => execFileSync(command, args, { cwd, encoding: "utf8", maxBuffer: 32 * 1024 * 1024 }).trim();

export function assignTasks(inventory, checks, durations = {}) {
  const tasks = new Map([["checks", { id: "checks", commands: checks }]]);
  const weights = [0, 0, 0, 0];
  const add = (id, item, names) => {
    if (!tasks.has(id)) tasks.set(id, { id, commands: [] });
    tasks.get(id).commands.push({ id: `${id}-${tasks.get(id).commands.length + 1}`, command: "go", goJSON: true,
      args: ["test", "-json", "-p", "1", "-count=1", `-timeout=${PACKAGE_TIMEOUT}`, "-run", names.length ? exact(names) : "^$", item.package],
      expectedTests: names.map(test => ({ package: item.importPath, test })) });
  };
  for (const item of inventory) {
    if (item.package !== "./internal/governance") {
      add(item.package === "./internal/cli" ? "cli" : item.package === "./internal/project" ? "project" : "checks", item, item.tests);
      continue;
    }
    const lifecycle = item.tests.filter(name => SLOW_TESTS.some(s => s.package === item.package && s.names.includes(name)));
    if (lifecycle.length) add("lifecycle", item, lifecycle);
    const groups = [[], [], [], []];
    const cost = name => durations[`${item.importPath}/${name}`] ?? (/Native|Handoff|Backend|Baseline/.test(name) ? 10 : 1);
    for (const name of item.tests.filter(n => !lifecycle.includes(n)).sort((a, b) => cost(b) - cost(a) || a.localeCompare(b))) {
      const index = weights.indexOf(Math.min(...weights));
      groups[index].push(name); weights[index] += cost(name);
    }
    for (const [i, names] of groups.entries()) if (names.length) add(`governance-${i + 1}`, item, names.sort());
    if (!item.tests.length) add("checks", item, []);
  }
  const result = [...tasks.values()];
  assertCoverage(inventory, result);
  return result;
}

export function assertCoverage(inventory, tasks) {
  const expected = new Set(inventory.flatMap(p => p.tests.map(t => `${p.importPath}/${t}`)));
  const observed = new Set(), ids = new Set();
  for (const task of tasks) {
    if (ids.has(task.id)) throw new Error(`重复任务: ${task.id}`);
    ids.add(task.id);
    for (const command of task.commands) for (const item of command.expectedTests ?? []) {
      const key = `${item.package}/${item.test}`;
      if (observed.has(key)) throw new Error(`重复测试: ${key}`);
      if (!expected.has(key)) throw new Error(`未选择的测试: ${key}`);
      observed.add(key);
    }
  }
  const missing = [...expected].filter(key => !observed.has(key));
  if (missing.length) throw new Error(`遗漏测试: ${missing.join(", ")}`);
}

export function discoverTests(groups, cwd) {
  const inventory = new Map();
  for (const group of groups) {
    const packages = readCommand("go", ["list", "-f", "{{.ImportPath}}", ...group.packages], cwd).split("\n").filter(Boolean);
    const module = readCommand("go", ["list", "-m"], cwd);
    for (const importPath of packages) {
      const pkg = importPath === module ? "." : "./" + importPath.slice(module.length + 1);
      if (!inventory.has(pkg)) inventory.set(pkg, { package: pkg, importPath, tests: new Set() });
      const listed = readCommand("go", ["test", "-list", group.run ?? ".", pkg], cwd);
      const names = listed.split("\n").filter(line => /^(Test|Example|Fuzz)\S*$/.test(line));
      if (group.run && !names.length) throw new Error(`定向选择未匹配测试: ${pkg} / ${group.run}`);
      names.forEach(name => inventory.get(pkg).tests.add(name));
    }
  }
  return [...inventory.values()].sort((a, b) => a.package.localeCompare(b.package)).map(p => ({ ...p, tests: [...p.tests].sort() }));
}

function sourceDigest(cwd) {
  const names = readCommand("git", ["ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd).split("\0").filter(Boolean);
  const digest = createHash("sha256");
  for (const name of [...new Set(names)].sort()) {
    const file = path.join(cwd, name);
    if (!fs.existsSync(file)) { digest.update(`${name}\0deleted\0`); continue; }
    const info = fs.lstatSync(file);
    digest.update(`${name}\0${info.mode & 0o111}\0`);
    if (info.isDirectory()) { digest.update("gitlink\0"); continue; }
    digest.update(info.isSymbolicLink() ? fs.readlinkSync(file) : fs.readFileSync(file));
    digest.update("\0");
  }
  return digest.digest("hex");
}

function inputs(cwd, templateRoot) {
  const templateRoots = templateRoot ? [templateRoot, ...["backend", "frontend", "design"].map(role => path.join(templateRoot, "submodules", `yss-harness-${role}-agent`))] : [];
  const templateSources = templateRoot ? templateRoots
    .map(root => ({ commit: readCommand("git", ["rev-parse", "HEAD"], root),
      sourceDigest: sourceDigest(root) })) : null;
  const aliases = [[process.env.YSS_NATIVE_BINARY, "@binary"], [templateRoot, "@template"], [cwd, "@cli"],
    [process.env.RUNNER_TEMP, "@temp"]].filter(([root]) => root).sort((a, b) => b[0].length - a[0].length);
  const normalize = value => aliases.reduce((result, [root, alias]) => result.split(root).join(alias), value);
  const environment = Object.entries(process.env).filter(([key]) => (key.startsWith("YSS_") && key !== "YSS_NATIVE_BINARY_SHA256") ||
    ["GOFLAGS", "GOTOOLCHAIN", "GOEXPERIMENT", "CGO_ENABLED", "GOOS", "GOARCH", "NODE_OPTIONS", "NODE_PATH", "PYTHONPATH", "PYTHONHOME"].includes(key))
    .sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => [key, normalize(value)]);
  const externalInputs = {};
  for (const key of ["YSS_LEGACY_SOURCE", "YSS_RETAINED_STAGE_MODULE", "YSS_RESEARCH_ORACLE_NODE", "YSS_PROFILE_GUIDANCE_REPORT",
    "YSS_PLAN_ORACLE_FIXTURE", "YSS_READING_ORACLE_FIXTURE", "YSS_LOCAL_PLAN_HANDOFF_ROOT", "YSS_BACKEND_COMPLETED_TASK_ROOT",
    "YSS_SCHEMA_CORPUS_ROOT", "YSS_NATIVE_SOURCE_ROOT", "YSS_LEGACY_ORACLE_ROOT"]) {
    const root = process.env[key];
    if (!root || root === cwd || root === templateRoot) continue;
    externalInputs[key] = fs.existsSync(root) ? externalInputDigest(root) : "missing";
  }
  return { cliCommit: readCommand("git", ["rev-parse", "HEAD"], cwd), sourceDigest: sourceDigest(cwd),
    sourceLockSha256: hash(fs.readFileSync(path.join(cwd, "docs/source-lock.json"))),
    templateSources,
    binarySha256: process.env.YSS_NATIVE_BINARY ? hash(fs.readFileSync(process.env.YSS_NATIVE_BINARY)) : null,
    platform: `${process.platform}/${process.arch}`,
    go: JSON.parse(readCommand("go", ["env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOFLAGS", "GOEXPERIMENT", "CGO_ENABLED"], cwd)), node: process.version,
    python: templateRoot ? JSON.parse(readCommand("python3", ["-c", "import sys,json,importlib.metadata as m; print(json.dumps([sys.version,{p:m.version(p) for p in ['jsonschema','referencing','jsonschema-specifications','rpds-py','attrs']}]))"], cwd)) : null,
    pnpm: templateRoot ? readCommand("pnpm", ["--version"], cwd) : null,
    nodeDependencies: templateRoots.map(root => dependencyDigest(path.join(root, ".template-source/tooling/node/node_modules"), normalize)),
    environmentSha256: hash(JSON.stringify(environment)), externalInputs };
}

function dependencyDigest(root, normalize) {
  if (!fs.existsSync(root)) return "missing";
  const digest = createHash("sha256");
  const visit = (file, relative) => {
    const info = fs.lstatSync(file);
    digest.update(`${relative}\0${info.mode & 0o111}\0`);
    if (info.isSymbolicLink()) digest.update(fs.readlinkSync(file));
    else if (info.isFile()) {
      const bytes = fs.readFileSync(file);
      digest.update(relative.split("/").includes(".bin") && !bytes.includes(0) ? normalize(bytes.toString("utf8")) : bytes);
    }
    else if (info.isDirectory()) for (const name of fs.readdirSync(file).sort()) {
      // pnpm's machine-local store/cache metadata is not executable input.
      if (relative === "." && [".modules.yaml", ".pnpm-workspace-state-v1.json"].includes(name)) continue;
      visit(path.join(file, name), relative + "/" + name);
    }
    else throw new Error(`依赖不是普通文件/目录: ${file}`);
    digest.update("\0");
  };
  visit(root, ".");
  return digest.digest("hex");
}

function externalInputDigest(root) {
  const digest = createHash("sha256"), visiting = new Set();
  const visit = (file, relative) => {
    const real = fs.realpathSync(file);
    if (visiting.has(real)) throw new Error(`验证输入包含循环链接: ${file}`);
    visiting.add(real);
    const info = fs.statSync(file);
    digest.update(`${relative}\0${info.mode & 0o111}\0`);
    if (info.isFile()) digest.update(fs.readFileSync(file));
    else if (info.isDirectory()) for (const name of fs.readdirSync(file).sort()) {
      if (name !== ".git" && name !== "node_modules") visit(path.join(file, name), relative + "/" + name);
    }
    else throw new Error(`验证输入不是普通文件/目录: ${file}`);
    digest.update("\0"); visiting.delete(real);
  };
  visit(root, ".");
  return digest.digest("hex");
}

export function buildTaskManifest(plan, checks, { cwd, templateRoot, durations } = {}) {
  const inventory = discoverTests(plan.go, cwd);
  const manifest = { schemaVersion: 1, inputs: inputs(cwd, templateRoot), plan, inventory,
    tasks: assignTasks(inventory, checks.map(c => ({ ...c, cwd: c.cwd === templateRoot ? "template" : "cli" })), durations) };
  return { ...manifest, digest: hash(JSON.stringify(manifest)) };
}

function validateManifest(manifest) {
  const { digest, ...body } = manifest;
  if (manifest.schemaVersion !== 1 || hash(JSON.stringify(body)) !== digest) throw new Error("验证任务清单摘要不一致");
  assertCoverage(manifest.inventory, manifest.tasks);
}

export async function executeTask(manifest, taskId, { cwd, templateRoot, outDir, failFast = false } = {}) {
  validateManifest(manifest);
  if (JSON.stringify(inputs(cwd, templateRoot)) !== JSON.stringify(manifest.inputs)) throw new Error("源码、工具、平台、模板或候选二进制变化，必须重新生成验证计划");
  const task = manifest.tasks.find(t => t.id === taskId);
  if (!task) throw new Error(`未知验证任务: ${taskId}`);
  const commands = task.commands.map(c => ({ ...c, cwd: c.cwd === "template" ? templateRoot : cwd,
    args: failFast && c.goJSON ? [c.args[0], "-failfast", ...c.args.slice(1)] : c.args }));
  const report = await runCommands(commands, { cwd, outDir, failFast });
  report.taskId = taskId; report.manifestDigest = manifest.digest;
  report.inputDrift = JSON.stringify(inputs(cwd, templateRoot)) !== JSON.stringify(manifest.inputs);
  if (report.inputDrift) report.exitCode ||= 1;
  fs.writeFileSync(path.join(outDir, "test-timings.json"), JSON.stringify(report, null, 2) + "\n");
  return report;
}

export function collectReports(manifest, reports) {
  validateManifest(manifest);
  const seen = new Set();
  for (const report of reports) {
    if (report.manifestDigest !== manifest.digest || seen.has(report.taskId) || !manifest.tasks.some(t => t.id === report.taskId)) throw new Error("报告来自不同计划、重复或未知任务");
    seen.add(report.taskId);
    const task = manifest.tasks.find(t => t.id === report.taskId);
    if (report.exitCode !== 0 || report.inputDrift !== false || report.commands.length !== task.commands.length || report.commands.some(c => c.status !== "passed")) throw new Error(`验证未通过: ${report.taskId}`);
    for (const [index, command] of task.commands.entries()) {
      const observed = report.commands[index];
      if (observed.id !== command.id || observed.command !== command.command ||
          JSON.stringify(observed.args.filter(a => a !== "-failfast")) !== JSON.stringify(command.args)) throw new Error(`执行命令不匹配: ${report.taskId}/${command.id}`);
      for (const expected of command.expectedTests ?? []) {
        const matches = report.tests.filter(t => t.command === command.id && t.package === expected.package && t.test === expected.test);
        if (matches.length !== 1 || !["pass", "skip"].includes(matches[0].status)) throw new Error(`测试未完成: ${expected.package}/${expected.test}`);
      }
    }
  }
  if (manifest.tasks.some(t => !seen.has(t.id))) throw new Error("验证报告缺失，不能通过");
  return { status: "passed", manifestDigest: manifest.digest, tasks: [...seen],
    skipped: reports.flatMap(r => r.tests.filter(t => t.status === "skip")) };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = {};
    for (let i = 2; i < process.argv.length; i++) {
      const flag = process.argv[i];
      if (flag === "--fail-fast") options.failFast = true;
      else if (["--manifest", "--task", "--report-dir", "--template-root", "--collect"].includes(flag) && process.argv[i + 1]) options[flag.slice(2)] = process.argv[++i];
      else throw new Error(`未知或缺值参数: ${flag}`);
    }
    const manifest = JSON.parse(fs.readFileSync(options.manifest));
    if (options.collect) {
      const cwd = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
      const templateRoot = options["template-root"] ?? process.env.YSS_LEGACY_ORACLE_ROOT;
      if (JSON.stringify(inputs(cwd, templateRoot)) !== JSON.stringify(manifest.inputs)) throw new Error("汇总时的源码、工具、模板或候选二进制与清单不一致");
      const reports = fs.readdirSync(options.collect, { recursive: true }).filter(f => f.endsWith("test-timings.json"))
        .map(f => JSON.parse(fs.readFileSync(path.join(options.collect, f))));
      console.log(JSON.stringify(collectReports(manifest, reports), null, 2));
    } else {
      const report = await executeTask(manifest, options.task, { cwd: path.resolve(path.dirname(fileURLToPath(import.meta.url)), ".."),
        templateRoot: options["template-root"] ?? process.env.YSS_LEGACY_ORACLE_ROOT, outDir: options["report-dir"], failFast: options.failFast });
      process.exitCode = report.exitCode;
    }
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
