#!/usr/bin/env node
// 一次性生成「模板 commit → 版本」对照表（internal/bundle/assets/version-table.json）。
// 来源：到 --through 为止，yss-cli 的 docs/source-lock.json 历史与内嵌 Bundle 历史里出现过的 (Profile, 模板 commit)。
// 这些来源早于语义化版本发布，所以按各 Profile 首次出现的先后顺序记为 4.0.0-pre.N（N 从 1 起）；
// 它们都低于 4.0.0，足够写「删除某批历史来源遗留文件」的版本区间规则。
// 之后发布的实例直接记录语义化版本，不再查表。用法：
//   node tools/version-table.mjs --through <yss-cli 提交>         只打印，不写文件
//   node tools/version-table.mjs --through <yss-cli 提交> --write  写入 internal/bundle/assets/version-table.json
import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gunzipSync } from "node:zlib";

const PROFILES = ["spec", "design", "backend"];
const BASE = "4.0.0";
const CLI_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const TABLE = path.join(CLI_ROOT, "internal/bundle/assets/version-table.json");
const LOCK = "docs/source-lock.json";
const PACKED = "internal/bundle/assets/bundles.json.gz";
const COMMIT = /^[a-f0-9]{40}$/;

// Exact pre-semver packages absent from top-level CLI Bundle history. The marker
// only places these sources below 4.0.0; it is not a historical release or ordinal.
const LEGACY_SOURCES = [
  {
    "profile": "design",
    "templateCommit": "b9f7923ccc0bd5d55443e49cb2fe5857d623c648",
    "version": "4.0.0-pre.legacy.1",
    "origin": {
      "kind": "fixed-legacy-package",
      "package": "create-yss-harness-design",
      "packageVersion": "0.8.17",
      "cliCommit": "7d2f9008d998ca87298a130f31019ffd6a87b3a5",
      "packageSha256": "de4762500e339041eefeba78162006c1cbdf761f343e6bb7b009965ae03b6386",
      "snapshotArchiveSha256": "8c549afc0e6726ffffc67f081676532d0d7533b50deeccba9738976e5931ab7d"
    }
  },
  {
    "profile": "backend",
    "templateCommit": "3eacf7ea0fd95377aef04e0e9ead2c3236be93e1",
    "version": "4.0.0-pre.legacy.1",
    "origin": {
      "kind": "fixed-legacy-package",
      "package": "create-yss-harness-backend",
      "packageVersion": "0.4.21",
      "cliCommit": "0799872b082fe7decea59db7eb6545692472d2d2",
      "packageSha256": "85aab48d6b233e962b8401ec8521194a919a633f454e4a23745c6d9a2a89380c",
      "snapshotArchiveSha256": "be58816309dde0e9de2c5ecda3f7fabba808246fc005f745c273425cf8c6050c"
    }
  }
];

function git(args, { binary = false } = {}) {
  const result = spawnSync("git", ["-C", CLI_ROOT, ...args], { maxBuffer: 256 * 1024 * 1024 });
  if (result.status !== 0) return null;
  return binary ? result.stdout : result.stdout.toString("utf8");
}

function showJson(commit, file, { gz = false } = {}) {
  const raw = git(["show", `${commit}:${file}`], { binary: true });
  if (!raw) return null;
  try {
    return JSON.parse((gz ? gunzipSync(raw) : raw).toString("utf8"));
  } catch {
    return null;
  }
}

/** 一个 yss-cli 提交里能读到的 (profile → 模板 commit) 集合；读不到的来源跳过。 */
export function commitsAt(commit) {
  const found = new Map();
  const add = (profile, value) => {
    if (PROFILES.includes(profile) && typeof value === "string" && COMMIT.test(value)) {
      if (!found.has(profile)) found.set(profile, new Set());
      found.get(profile).add(value);
    }
  };
  const lock = showJson(commit, LOCK);
  for (const [profile, source] of Object.entries(lock?.profiles ?? {})) add(profile, source?.templateCommit);
  const packed = showJson(commit, PACKED, { gz: true });
  for (const [profile, bundle] of Object.entries(packed?.profiles ?? {})) add(profile, bundle?.templateCommit);
  for (const profile of PROFILES) add(profile, showJson(commit, `internal/bundle/assets/${profile}.json.gz`, { gz: true })?.templateCommit);
  return found;
}

/** 按提交先后顺序，给每个 Profile 首次出现的模板 commit 依次编号。 */
export function buildTable(through) {
  const listing = git(["log", "--reverse", "--format=%H", through, "--", LOCK, PACKED, "internal/bundle/assets"]);
  if (listing === null) throw new Error(`无法读取 ${through} 之前的历史`);
  const seen = new Map(PROFILES.map((profile) => [profile, new Map()]));
  for (const commit of listing.split("\n").filter(Boolean)) {
    for (const [profile, commits] of commitsAt(commit)) {
      for (const templateCommit of [...commits].sort()) {
        if (!seen.get(profile).has(templateCommit)) seen.get(profile).set(templateCommit, commit);
      }
    }
  }
  const entries = [];
  for (const profile of PROFILES) {
    let ordinal = 0;
    for (const [templateCommit, firstSeen] of seen.get(profile)) {
      ordinal += 1;
      entries.push({ profile, templateCommit, version: `${BASE}-pre.${ordinal}`, firstSeen });
    }
  }
  return { schemaVersion: 1, base: BASE, through, entries: [...entries, ...LEGACY_SOURCES.filter(entry => !seen.get(entry.profile).has(entry.templateCommit))] };
}

function render(table) {
  const lines = table.entries.map((entry) => `    ${JSON.stringify(entry)}`);
  return `{\n  "schemaVersion": ${table.schemaVersion},\n  "base": ${JSON.stringify(table.base)},\n  "through": ${JSON.stringify(table.through)},\n  "entries": [\n${lines.join(",\n")}\n  ]\n}\n`;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const read = (name) => (args.includes(name) ? args[args.indexOf(name) + 1] : undefined);
  try {
    const rawThrough = read("--through");
    if (!rawThrough) throw new Error("需要 --through <yss-cli 提交>：固定覆盖到哪个提交，之后的历史不再进入对照表");
    const through = git(["rev-parse", "--verify", `${rawThrough}^{commit}`])?.trim();
    if (!through) throw new Error(`无法解析提交 ${rawThrough}`);
    const text = render(buildTable(through));
    if (args.includes("--write")) {
      const existing = readFileSync(TABLE, "utf8");
      if (existing !== text && JSON.parse(existing).entries.length > 0) throw new Error("对照表已有内容；这是一次性生成，不覆盖。确需重做先人工清空 entries");
      writeFileSync(TABLE, text);
      process.stdout.write(`已写入 ${path.relative(CLI_ROOT, TABLE)}\n`);
    } else {
      process.stdout.write(text);
    }
  } catch (error) {
    process.stderr.write(`version-table 错误: ${error.message}\n`);
    process.exitCode = 1;
  }
}
