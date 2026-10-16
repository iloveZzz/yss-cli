// 正式合同模式的对拍场景：用模板仓自带的测试夹具（合成的批准合同、架构决定、用户决定、设计前置与批准记录）
// 构造治理根，再让 Node 基准与 Go 实现分别消费同一份输入。夹具只用于机制对拍，不代表任何真实批准。
//
// 平台已验证证据在固定模板提交上与生成器源码指纹漂移（`requalify candidate`），所以真实目录的
// verified 路径在两侧都应一致地拒绝；成功路径只能走候选模式（Node 的 generate-candidate 夹具目录，
// Go 对应 Options.PlatformCatalog + AllowCandidate，仅对拍驱动使用，CLI 不暴露）。
import {createHash} from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import {pathToFileURL} from 'node:url';

const SKILLS = {ddd: 'yss-ddd-scaffold-generator', mvc: 'yss-layered-mvc-scaffold-generator'};
const COMMANDS = ['./mvnw validate', './mvnw test', './mvnw package'];
const DDD_MODULES = ['domain', 'application', 'infrastructure', 'adapter', 'bootstrap'];
const MVC_CAPABILITIES = {
  basic: {capabilities: [], modules: ['server', 'service', 'repository']},
  adapter: {capabilities: ['external-integration'], modules: ['server', 'service', 'repository', 'adapter']},
  client: {capabilities: ['published-client'], modules: ['server', 'service', 'repository', 'client']},
  feign: {capabilities: ['feign-client'], modules: ['server', 'service', 'repository', 'client', 'feign-client']},
  combined: {capabilities: ['external-integration', 'feign-client'], modules: ['server', 'service', 'repository', 'adapter', 'client', 'feign-client']},
};
const PLATFORMS = {
  'boot2': {platform: 'spring-boot-2.7-jdk8', namespace: 'javax', version: '2.7.18'},
  'boot3': {platform: 'spring-boot-3.5-jdk17', namespace: 'jakarta', version: '3.5.16'},
};

const importFrom = (templateRoot, relative) => import(pathToFileURL(path.join(templateRoot, relative)).href);
const digest = text => `sha256:${createHash('sha256').update(text).digest('hex')}`;

async function modules(templateRoot) {
  const [decision, design, example, platform, scaffold] = await Promise.all([
    importFrom(templateRoot, 'scripts/fixtures/user-decision/build-fixture.mjs'),
    importFrom(templateRoot, 'scripts/fixtures/backend-scaffold/design-prerequisites.mjs'),
    importFrom(templateRoot, 'scripts/lib/scaffold-reference-example.mjs'),
    importFrom(templateRoot, 'scripts/fixtures/backend-scaffold/platform-fixture.mjs'),
    importFrom(templateRoot, 'scripts/lib/backend-platform.mjs'),
  ]);
  return {attachScaffoldDecisionFixture: decision.attachScaffoldDecisionFixture, attachDesignPrerequisites: design.attachDesignPrerequisites, referenceExample: example.referenceExample, fixtureCatalog: platform.fixtureCatalog, backend: scaffold};
}

function decisionRecord(spec) {
  const ddd = spec.arch === 'ddd';
  return {
    decision_id: 'scaffold-decision.demo-service', project_id: 'demo-service', parent_project_id: null, status: 'lifecycle-approved',
    recommended_architecture: ddd ? 'domain-driven' : 'layered-mvc', recommendation_reasons: [ddd ? '存在复杂领域规则' : '薄 CRUD 服务'],
    confirmed_architecture: ddd ? 'domain-driven' : 'layered-mvc', override_reason: null, inheritance_mode: 'root-default', inherited_from: null,
    platform_profile: spec.platform.platform, architecture_profile: ddd ? 'target-domain-model' : 'layered-mvc-service',
    verification_database: 'h2', production_database: 'not-bound',
    requested_capabilities: ddd ? [] : spec.capability.capabilities, resolved_modules: ddd ? DDD_MODULES : spec.capability.modules, resolution_version: 1,
    user_confirmation: {confirmed_by: 'tester', channel: 'test', confirmation_ref: 'test://confirmation', confirmed_at: '2026-09-05T00:00:00Z', normalized_text: ddd ? '确认使用 DDD' : '确认使用 MVC'},
    decision_inputs_digest: `sha256:${'1'.repeat(64)}`,
  };
}

function contractRecord(spec, outputDir, decisionRef, decisionDigest) {
  const ddd = spec.arch === 'ddd';
  const skill = SKILLS[spec.arch];
  const modules = ddd ? DDD_MODULES : spec.capability.modules;
  return {
    schema_version: spec.schemaVersion,
    ...(spec.schemaVersion >= 5 ? {reference_example: spec.referenceExample} : {}),
    kind: 'project-scaffold-contract', delivery_role: 'backend', scaffold_kind: ddd ? 'backend-ddd' : 'backend-layered-mvc',
    repository_scope: 'external-repository', init_git: false,
    contract_id: 'scaffold.demo-service.v1', contract_version: 1, scaffold_request_id: 'request.demo-service', status: 'approved',
    compiler_draft_ref: 'compiler://demo/1', lifecycle_approval_ref: 'approval://demo/1', persisted_ref: 'contract://demo/1', current_version: true,
    implementation_repository: 'external', backend_repository: 'external', scaffold_status: 'required',
    project_name: 'demo-service', target_output_dir: outputDir, base_package: 'com.yss.demo',
    architecture_family: ddd ? 'domain-driven' : 'layered-mvc', architecture_profile: ddd ? 'target-domain-model' : 'layered-mvc-service', generator_skill: skill,
    decision_ref: decisionRef, decision_id: 'scaffold-decision.demo-service', decision_digest: decisionDigest,
    maven_coordinates: spec.coordinates,
    profiles: ddd
      ? {architecture: 'target-domain-model', persistence: 'mybatis-plus', verification_database: 'h2', production_database: 'not-bound', platform: spec.platform.platform, validation_namespace: spec.platform.namespace, dto_placement: 'web', repository: 'yss-internal'}
      : {architecture: 'layered-mvc', persistence: 'mybatis-plus', verification_database: 'h2', production_database: 'not-bound', platform: spec.platform.platform, validation_namespace: spec.platform.namespace, repository: 'yss-internal'},
    module_profile: {requested_capabilities: ddd ? [] : spec.capability.capabilities, resolved_modules: modules, resolution_version: 1},
    allowed_write_paths: ['.'], expected_evidence_files: ['.yss/scaffold-generation.json'], verification_commands: COMMANDS,
    approval: {approval_ref: 'approval://demo/1', approver: 'lifecycle', persisted_ref: 'contract://demo/1', current_version: 1},
    work_unit: {id: 'scaffold.demo', behavior: 'project-scaffold', primary_skill: skill, supporting_skills: ['yss-implementation-contract-compiler'], tdd_mode: 'controlled-generation', allowed_write_paths: ['.'], expected_evidence: ['.yss/scaffold-generation.json'], verification_commands: COMMANDS, controlled_generation: true},
    generation_policy: {mode: 'initialize-only', existing_target: 'unsupported', old_project_migration: 'unsupported', template_upgrade: 'unsupported'},
  };
}

// 在 base/gov 下构造治理根、合同与候选目录；返回对拍需要的选项与候选目录文件。
async function prepare(spec, context, base, outputDir) {
  const m = context.modules ??= await modules(context.templateRoot);
  const {makeRoot} = await importFrom(context.templateRoot, 'tests/helpers/yss-instance.mjs');
  const root = makeRoot('yss-scaffold-parity-gov-');
  fs.cpSync(root, path.join(base, 'gov'), {recursive: true});
  fs.rmSync(root, {recursive: true, force: true});
  const gov = path.join(base, 'gov');
  // 治理校验读取的是治理根里的技能资产（真实项目由 skills ensure 安装）；夹具根只带脚本，这里补齐设计校验所需的技能目录。
  for (const skill of ['yss-technical-design', 'yss-tactical-design', 'yss-dto', 'yss-ddd-scaffold-generator', 'yss-layered-mvc-scaffold-generator']) {
    fs.cpSync(path.join(context.templateRoot, '.agents/skills', skill), path.join(gov, '.agents/skills', skill), {recursive: true, force: true});
  }
  const decisionFile = path.join(gov, 'scaffold-architecture-decisions.yaml');
  const decisionSet = {schema_version: 1, kind: 'scaffold-architecture-decisions', template: false, status: 'current', decisions: [decisionRecord(spec)]};
  decisionSet.decisions = decisionSet.decisions.map(decision => m.attachScaffoldDecisionFixture(path.join(gov, 'user-decision'), decision));
  const decisionText = `${JSON.stringify(decisionSet, null, 2)}\n`;
  fs.writeFileSync(decisionFile, decisionText);
  const contract = contractRecord(spec, outputDir, spec.arch === 'ddd' ? 'scaffold-architecture-decisions.yaml' : decisionFile, digest(decisionText));
  if (spec.realBinding) {
    const catalog = m.backend.loadBackendPlatforms();
    const entry = catalog.compatibility.find(item => item.status === 'verified' && item.profile_id === spec.platform.platform);
    contract.platform_configuration = m.backend.platformBinding(m.backend.platformProfile(entry.profile_id, catalog, entry.spring_boot_version), entry);
  }
  m.attachDesignPrerequisites(gov, contract, {dataImpact: spec.dataImpact ?? 'not-applicable', apiImpact: spec.apiImpact ?? 'not-applicable'});
  spec.mutate?.(contract, {gov, decisionFile});
  const contractFile = path.join(gov, 'contract.json');
  fs.writeFileSync(contractFile, `${JSON.stringify(contract, null, 2)}\n`);
  const catalogFile = path.join(base, 'candidate-catalog.json');
  fs.writeFileSync(catalogFile, JSON.stringify(m.fixtureCatalog(contract)));
  const options = {
    projectName: 'demo-service', basePackage: 'com.yss.demo', contractFile, contractId: contract.contract_id, contractVersion: '1',
    approvalRef: contract.lifecycle_approval_ref, compilerDraftRef: contract.compiler_draft_ref, persistedRef: contract.persisted_ref,
    groupId: contract.maven_coordinates.group_id, projectVersion: contract.maven_coordinates.project_version,
    parentGroupId: contract.maven_coordinates.parent.group_id, parentArtifactId: contract.maven_coordinates.parent.artifact_id,
    parentVersion: contract.maven_coordinates.parent.version, yssComponentsVersion: contract.maven_coordinates.yss_components_version,
    ...(spec.optionOverrides ?? {}),
  };
  return {options, candidateCatalog: spec.realBinding ? undefined : catalogFile};
}

const FIXTURE_COORDINATES = {group_id: 'com.yss.demo', project_version: '1.0.0-SNAPSHOT', parent: {group_id: 'com.yss.cloud', artifact_id: 'yss-cloud-microservice', version: '2.0.0-SNAPSHOT'}, yss_components_version: '2.0.0-SNAPSHOT'};
const REAL_COORDINATES = {group_id: 'com.yss.demo', project_version: '1.0.0-SNAPSHOT', parent: {group_id: 'com.yss.cloud', artifact_id: 'yss-cloud-microservice', version: '3.2.0-SNAPSHOT'}, yss_components_version: '3.2.0-SNAPSHOT'};

export async function formalScenarios(context, {templateRoot}) {
  context.templateRoot = templateRoot;
  const list = [];
  const add = (id, title, spec, extra = {}) => list.push({
    id, title, arch: spec.arch, kind: 'formal', expect: 'success',
    candidate: !spec.realBinding,
    spec: {schemaVersion: 4, platform: PLATFORMS.boot2, coordinates: FIXTURE_COORDINATES, capability: MVC_CAPABILITIES.basic, ...spec},
    ...extra,
  });
  const reject = (id, title, spec, extra = {}) => add(id, title, spec, {expect: 'failure', ...extra});

  for (const arch of ['ddd', 'mvc']) {
    add(`formal-${arch}-v4-boot2`, `${arch} 正式合同 v4（候选目录，Boot 2.7/JDK8）`, {arch});
    add(`formal-${arch}-v4-boot3`, `${arch} 正式合同 v4（候选目录，Boot 3.5/JDK17）`, {arch, platform: PLATFORMS.boot3});
    add(`formal-${arch}-v5-example-on`, `${arch} 合同 v5 启用参考示例`, {arch, schemaVersion: 5, referenceExample: 'on'});
    add(`formal-${arch}-v5-example-off`, `${arch} 合同 v5 禁用参考示例`, {arch, schemaVersion: 5, referenceExample: 'off'});
    add(`formal-${arch}-data-api-required`, `${arch} 数据与 API 决定均为 required`, {arch, dataImpact: 'required', apiImpact: 'required'});
    add(`formal-${arch}-data-required`, `${arch} 仅数据决定 required`, {arch, dataImpact: 'required'});
    add(`formal-${arch}-api-required`, `${arch} 仅 API 决定 required`, {arch, apiImpact: 'required'});
    add(`formal-${arch}-verified-path`, `${arch} 绑定真实 verified 目录条目（当前证据漂移，两侧须一致）`, {arch, platform: PLATFORMS.boot3, coordinates: REAL_COORDINATES, realBinding: true}, {expect: 'agree'});

    reject(`formal-${arch}-reject-candidate-in-production`, `${arch} 候选目录合同在真实目录下被拒绝`, {arch}, {candidate: false});
    reject(`formal-${arch}-reject-contract-id`, `${arch} --contract-id 与合同不一致`, {arch, optionOverrides: {contractId: 'other'}});
    reject(`formal-${arch}-reject-contract-version`, `${arch} --contract-version 与合同不一致`, {arch, optionOverrides: {contractVersion: '2'}});
    reject(`formal-${arch}-reject-approval-ref`, `${arch} --approval-ref 与合同不一致`, {arch, optionOverrides: {approvalRef: 'approval://other'}});
    reject(`formal-${arch}-reject-persisted-ref`, `${arch} --persisted-ref 与合同不一致`, {arch, optionOverrides: {persistedRef: 'contract://other'}});
    reject(`formal-${arch}-reject-project-name`, `${arch} --project-name 与合同不一致`, {arch, optionOverrides: {projectName: 'other-service'}});
    reject(`formal-${arch}-reject-maven-coordinate`, `${arch} Maven 坐标与合同不一致`, {arch, optionOverrides: {groupId: 'com.other'}});
    reject(`formal-${arch}-reject-platform-argument`, `${arch} 正式模式不接受独立平台参数`, {arch, optionOverrides: {platformProfile: 'spring-boot-2.7-jdk8'}});
    reject(`formal-${arch}-reject-status`, `${arch} 合同未批准`, {arch, mutate: contract => { contract.status = 'draft'; }});
    reject(`formal-${arch}-reject-not-current`, `${arch} 合同不是当前版本`, {arch, mutate: contract => { contract.current_version = false; }});
    reject(`formal-${arch}-reject-write-path`, `${arch} 项目根不在 allowed_write_paths 内`, {arch, mutate: contract => { contract.allowed_write_paths = ['elsewhere']; contract.work_unit.allowed_write_paths = ['elsewhere']; }});
    reject(`formal-${arch}-reject-commands`, `${arch} 验证命令被改写`, {arch, mutate: contract => { contract.verification_commands = ['./mvnw test']; contract.work_unit.verification_commands = ['./mvnw test']; }});
    reject(`formal-${arch}-reject-schema-version`, `${arch} 旧 schema 版本`, {arch, mutate: contract => { contract.schema_version = 3; }});
    reject(`formal-${arch}-reject-decision-digest`, `${arch} 架构决定摘要漂移`, {arch, mutate: contract => { contract.decision_digest = `sha256:${'0'.repeat(64)}`; }});
    reject(`formal-${arch}-reject-output-dir`, `${arch} target_output_dir 与 --output-dir 不一致`, {arch, mutate: contract => { contract.target_output_dir = path.join(contract.target_output_dir, 'elsewhere'); }});
    reject(`formal-${arch}-reject-reference-drift`, `${arch} v5 参考示例摘要漂移`, {arch, schemaVersion: 5, referenceExample: 'drift'});
  }
  // MVC 的能力闭包：每个能力组合各一个场景。
  for (const [name, capability] of Object.entries(MVC_CAPABILITIES)) {
    if (name === 'basic') continue;
    add(`formal-mvc-capability-${name}`, `mvc 能力 ${name}（${capability.modules.join('/')}）`, {arch: 'mvc', capability});
  }
  add('formal-mvc-capability-combined-boot3', 'mvc 全能力组合（Boot 3.5/JDK17）', {arch: 'mvc', capability: MVC_CAPABILITIES.combined, platform: PLATFORMS.boot3});
  reject('formal-mvc-reject-capability-closure', 'mvc 模块闭包与能力不一致', {arch: 'mvc', capability: MVC_CAPABILITIES.client, mutate: contract => { contract.module_profile.resolved_modules = ['server', 'service', 'repository']; }});

  for (const scenario of list) scenario.prepare = (base, outputDir, side) => prepareScenario(scenario, context, base, outputDir, side);
  return list;
}

const prepared = new Map();

async function prepareScenario(scenario, context, base, outputDir) {
  const key = scenario.id;
  if (!prepared.has(key)) {
    const spec = {...scenario.spec};
    if (spec.referenceExample !== undefined) {
      const m = context.modules ??= await modules(context.templateRoot);
      const value = m.referenceExample(spec.referenceExample !== 'off');
      spec.referenceExample = spec.referenceExample === 'drift' ? {...value, template_digest: `sha256:${'0'.repeat(64)}`} : value;
    }
    prepared.set(key, await prepare(spec, context, base, outputDir));
  }
  return prepared.get(key);
}
