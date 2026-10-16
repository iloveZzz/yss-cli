#!/usr/bin/env node
// 静态护栏：治理规则以 Go 语义为准，不得在 Go 代码里把 Node 脚本的 SHA-256 写成常量去“钉字节”。
// 非测试 Go 源码（internal/、cmd/）出现 64 位十六进制字面量即失败。需要内嵌资产摘要时，
// 用 --allow 的路径白名单显式登记，并在 docs/governance-convergence.md 说明理由。
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const allow = new Set(process.argv.flatMap((v, i, a) => a[i - 1] === '--allow' ? [v] : []));
const literal = /(?<![0-9A-Fa-f])[0-9A-Fa-f]{64}(?![0-9A-Fa-f])/;
const found = [];
const walk = dir => {
  for (const entry of fs.readdirSync(dir, {withFileTypes: true})) {
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(file);
    else if (entry.name.endsWith('.go') && !entry.name.endsWith('_test.go')) {
      const ref = path.relative(root, file).split(path.sep).join('/');
      if (allow.has(ref)) continue;
      fs.readFileSync(file, 'utf8').split('\n').forEach((line, i) => { if (literal.test(line)) found.push(`${ref}:${i + 1}`); });
    }
  }
};
for (const dir of ['internal', 'cmd']) walk(path.join(root, dir));
if (found.length) {
  console.error('发现 64 位十六进制字面量（疑似固定摘要绑定），请改为语义校验：\n  ' + found.join('\n  '));
  process.exit(1);
}
console.log('check-no-pinned-digests: OK');
