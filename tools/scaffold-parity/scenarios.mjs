// 对拍场景矩阵。每个场景给出 Node 生成器与 Go 实现共同使用的选项；harness 负责在乾净目录分别运行并比较。
import fs from 'node:fs';
import path from 'node:path';
import {git} from './lib.mjs';

const KEBAB = {
  projectName: '--project-name', basePackage: '--base-package', outputDir: '--output-dir',
  platformProfile: '--platform-profile', springBootVersion: '--spring-boot-version', javaVersion: '--java-version',
  groupId: '--group-id', projectVersion: '--project-version', parentGroupId: '--parent-group-id',
  parentArtifactId: '--parent-artifact-id', parentVersion: '--parent-version', yssComponentsVersion: '--yss-components-version',
  contractFile: '--contract-file', contractId: '--contract-id', contractVersion: '--contract-version',
  approvalRef: '--approval-ref', compilerDraftRef: '--compiler-draft-ref', persistedRef: '--persisted-ref',
};

export function flags(options) {
  return Object.entries(KEBAB).flatMap(([key, flag]) => (options[key] === undefined ? [] : [flag, String(options[key])]));
}

export const BASE = Object.freeze({
  projectName: 'order-service', basePackage: 'com.yss.orders',
  platformProfile: 'spring-boot-3.5-jdk17', springBootVersion: '3.5.16', javaVersion: '17',
  groupId: 'com.yss', projectVersion: '1.0.0-SNAPSHOT',
  parentGroupId: 'com.yss.cloud', parentArtifactId: 'yss-cloud-microservice', parentVersion: '3.2.0-SNAPSHOT',
  yssComponentsVersion: '3.2.0-SNAPSHOT',
});

// 平台目录来自被测模板源，每个 Profile × 架构各一个场景，随目录自动增减。
export function platformMatrix(catalog) {
  return catalog.profiles.map(profile => ({
    platformProfile: profile.id, springBootVersion: profile.spring_boot_version, javaVersion: String(profile.java_version),
  }));
}

function gitRepositoryWithMount(mount) {
  return directory => {
    git(directory, ['init', '-q']);
    fs.writeFileSync(path.join(directory, '.gitmodules'), `[submodule "backend/${mount}"]\n\tpath = backend/${mount}\n\turl = https://example.invalid/${mount}.git\n`);
    git(directory, ['update-index', '--add', '--cacheinfo', `160000,${'a'.repeat(40)},backend/${mount}`]);
  };
}

export function standaloneScenarios(catalog) {
  const list = [];
  const add = (id, title, arch, options, extra = {}) => list.push({id, title, arch, kind: 'standalone', options: {...BASE, ...options}, expect: 'success', ...extra});
  const fail = (id, title, arch, options, extra = {}) => add(id, title, arch, options, {expect: 'failure', ...extra});
  for (const arch of ['ddd', 'mvc']) {
    for (const platform of platformMatrix(catalog)) {
      add(`${arch}-${platform.platformProfile}`, `${arch} 平台 ${platform.platformProfile} ${platform.springBootVersion}`, arch, platform);
    }
    add(`${arch}-names-short`, `${arch} 单字符项目名与单段包名`, arch, {projectName: 'a', basePackage: 'app'});
    add(`${arch}-names-digits`, `${arch} 含数字与连字符的项目名、深层包名`, arch, {projectName: 'a1-b2-c3-service', basePackage: 'com.yss.a1.b2.c3d4'});
    add(`${arch}-coordinates-release`, `${arch} 正式版本与限定符坐标`, arch, {groupId: 'com.example_org', projectVersion: '2.5.1', parentGroupId: 'com.example-org.cloud', parentArtifactId: 'parent_pom-1', parentVersion: '1.2.3-RC1', yssComponentsVersion: '9.8.7-beta.2'});
    add(`${arch}-nested-output`, `${arch} 输出目录的父级不存在时自动创建`, arch, {}, {outputSubdir: 'deep/er/output'});
    add(`${arch}-example-boot3`, `${arch} 独立模式启用参考示例（Boot 3.5/JDK17）`, arch, {}, {example: true});
    add(`${arch}-example-boot2`, `${arch} 独立模式启用参考示例（Boot 2.7/JDK8）`, arch, {platformProfile: 'spring-boot-2.7-jdk8', springBootVersion: '2.7.18', javaVersion: '8'}, {example: true});
    add(`${arch}-example-boot4`, `${arch} 独立模式启用参考示例（Boot 4.1/JDK21）`, arch, {platformProfile: 'spring-boot-4.1-jdk21', springBootVersion: '4.1.1', javaVersion: '21'}, {example: true});
    add(`${arch}-in-git-repo`, `${arch} 在普通 Git 仓库内生成`, arch, {}, {outputSubdir: 'backend', setup: directory => git(directory, ['init', '-q']), exclude: ['.git']});

    fail(`${arch}-reject-existing-target`, `${arch} 目标目录已存在时拒绝且不改动`, arch, {}, {setup: directory => fs.mkdirSync(path.join(directory, 'order-service'))});
    fail(`${arch}-reject-gitlink-mount`, `${arch} 目标是 git-submodule 挂载点时拒绝`, arch, {}, {outputSubdir: 'backend', setup: gitRepositoryWithMount('order-service'), exclude: ['.git']});
    fail(`${arch}-reject-unknown-profile`, `${arch} 未知平台 Profile`, arch, {platformProfile: 'spring-boot-9.9-jdk99'});
    fail(`${arch}-reject-boot-patch`, `${arch} Spring Boot 补丁版本与目录不一致`, arch, {springBootVersion: '3.5.1'});
    fail(`${arch}-reject-java-mismatch`, `${arch} Java 版本与配方不一致`, arch, {javaVersion: '21'});
    fail(`${arch}-reject-version-range`, `${arch} 版本范围或 x 通配`, arch, {parentVersion: '3.2.x'});
    fail(`${arch}-reject-latest`, `${arch} 版本为 latest`, arch, {yssComponentsVersion: 'latest'});
    fail(`${arch}-reject-project-name`, `${arch} 项目名不是 kebab-case`, arch, {projectName: 'OrderService'});
    fail(`${arch}-reject-base-package`, `${arch} 包名含大写`, arch, {basePackage: 'com.Yss.orders'});
    fail(`${arch}-reject-coordinate-chars`, `${arch} Maven 坐标含非法字符`, arch, {groupId: 'com yss'});
  }
  return list;
}
