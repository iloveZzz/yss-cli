"use strict";

const { spawnSync } = require("node:child_process");
const API_VERSION = 1;
const PROTOCOL_VERSION = 1;
const ERROR_RULES = [
  [/模板路径越界|中间符号链接|目标父路径不是目录|符号链接或特殊文件/, "YSS_PATH_SAFETY"],
  [/gitlink|git-submodule|detached HEAD|submodule/, "YSS_GIT_PROTECTED"],
  [/模板家族|家族身份|多个模板家族|目标身份属于|profile 身份|身份不一致|身份文件格式非法|身份 metadata schema|未知或非法 profile|不支持跨家族迁移/, "YSS_FAMILY_IDENTITY_INVALID"],
  [/ownership policy|user-owned|被 ownership policy 标记为 protected/, "YSS_OWNERSHIP_PROTECTED"],
  [/模板快照|snapshotHash|templateCommit/, "YSS_SNAPSHOT_INVALID"],
  [/模板元数据|metadataSchemaVersion|managedFilesManifestVersion/, "YSS_METADATA_INVALID"],
  [/yss-project\.yaml|repository_mode|schema_version/, "YSS_IDENTITY_INVALID"],
  [/迁移冲突|迁移目标|旧路径迁移冲突/, "YSS_MIGRATION_CONFLICT"],
  [/unsafe|受管路径阻断|人工整理 Ticket/, "YSS_UNSAFE_PATH"],
  [/不支持的参数|需要 --|需要一个值|--dry-run 与 --apply|必须显式传入/, "YSS_ARGUMENT_INVALID"],
  [/目标目录|目标路径/, "YSS_TARGET_INVALID"],
];

function toErrorEnvelope(error) {
  const message = String(error?.message || error || "未知错误");
  const code = typeof error?.code === "string" && error.code.startsWith("YSS_")
    ? error.code : ERROR_RULES.find(([pattern]) => pattern.test(message))?.[1] || "YSS_COMMAND_FAILED";
  return { schemaVersion: 1, ok: false, error: { code, message } };
}
function failure(code, message, extra) {
  return Object.assign(new Error(message), { code: `YSS_${code}`, ...extra });
}
function invoke(method, request) {
  let input;
  try { input = JSON.stringify(request ?? {}); }
  catch (error) { throw failure("ARGUMENT_INVALID", `API 请求无法序列化: ${error.message}`); }
  if (input === undefined || Buffer.byteLength(input) > 4 * 1024 * 1024) throw failure("ARGUMENT_INVALID", "API 请求必须可序列化且不超过 4 MiB");
  const executable = process.env.YSS_BINARY || "yss";
  const child = spawnSync(executable, ["compat-api", method], {
    input, encoding: "utf8", shell: false, windowsHide: true,
    timeout: 120000, maxBuffer: 32 * 1024 * 1024,
  });
  if (child.error) throw failure("RUNTIME_UNAVAILABLE", `Go CLI 无法执行: ${child.error.message}`, { cause: child.error });
  if (child.signal || child.status === null) throw failure("RUNTIME_UNAVAILABLE", "Go CLI 未正常退出");
  let envelope;
  try { envelope = JSON.parse(child.stdout); }
  catch { throw failure("PROTOCOL_MISMATCH", "Go CLI 未返回有效的协议 1 JSON"); }
  if (!envelope || envelope.outputVersion !== 1 || envelope.protocolVersion !== PROTOCOL_VERSION || !["ok", "error"].includes(envelope.status) || typeof envelope.code !== "string") {
    throw failure("PROTOCOL_MISMATCH", "Go CLI 输出协议不兼容");
  }
  if (envelope.status === "error") {
    if (child.status === 0) throw failure("PROTOCOL_MISMATCH", "错误 envelope 与退出码不一致");
    const legacy = envelope.result?.legacyError;
    throw failure(envelope.code, legacy?.error?.message || envelope.result?.message || envelope.code,
      { envelope: legacy || toErrorEnvelope(failure(envelope.code, envelope.result?.message)), nativeEnvelope: envelope });
  }
  if (child.status !== 0) throw failure("PROTOCOL_MISMATCH", "成功 envelope 与退出码不一致");
  return envelope;
}
function legacyCall(method, options = {}) {
  const result = invoke(method, options).result;
  // Reject a native Plan accidentally returned as the old Spec Plan v1.
  const operation = method === "projectDoctor" ? "doctor" : "sync";
  if (!result || result.schemaVersion !== 1 || result.operation !== operation) {
    throw failure("PROTOCOL_MISMATCH", "Go CLI 结果不是旧 Spec API v1 schema");
  }
  return result;
}
function projectDoctor(options) { return legacyCall("projectDoctor", options); }
function templatePlan(options) { return legacyCall("templatePlan", options); }
function projectDiff(options) { return legacyCall("projectDiff", options); }
function templateApply(options) { return legacyCall("templateApply", options); }

// This explicit new namespace consumes native JSON; it makes no legacy parity claim.
const native = Object.freeze({
  PROTOCOL_VERSION,
  run(request) { return invoke("native.run", request); },
  snapshot(profile = "spec") { return invoke("native.snapshot", { profile }); },
});
module.exports = { API_VERSION, projectDoctor, projectDiff, templatePlan, templateApply, toErrorEnvelope, native };
