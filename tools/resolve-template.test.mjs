import {test} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {execFileSync} from 'node:child_process';
import {createSnapshot} from './resolve-template.mjs';

test('正式 tag 只解析一次并固定两份策略；候选明确不冒充正式 tag', t => {
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), 'yss-tag-source-'));
  t.after(() => fs.rmSync(root, {recursive:true,force:true}));
  const git = (...args) => execFileSync('git', args, {cwd:root,encoding:'utf8'}).trim();
  git('init','-q'); git('config','user.email','test@example.org'); git('config','user.name','Test');
  for (const ref of ['.template-source/distribution/bundle-profile.json','profiles/backend/.template-source/distribution/bundle-profile.json']) {
    fs.mkdirSync(path.dirname(path.join(root,ref)), {recursive:true}); fs.writeFileSync(path.join(root,ref), ref);
  }
  git('add','.'); git('commit','-qm','old'); git('tag','v5.0.0'); const old = git('rev-parse','HEAD');
  fs.writeFileSync(path.join(root,'.template-source/distribution/bundle-profile.json'),'new');
  git('commit','-qam','new'); const newer = git('rev-parse','HEAD');
  const official = createSnapshot({sourceRoot:root,tag:'v5.0.0',remote:root});
  assert.equal(official.profiles.spec.templateCommit,old);
  assert.equal(official.profiles.backend.templateTag,'v5.0.0');
  const candidate = createSnapshot({sourceRoot:root,tag:'v5.0.0',candidateCommit:newer,remote:root});
  assert.equal(candidate.profiles.spec.templateCommit,newer);
  assert.equal(candidate.profiles.spec.templateTag,undefined);
  assert.notEqual(candidate.profiles.spec.policyHash,official.profiles.spec.policyHash);
  assert.throws(() => createSnapshot({sourceRoot:root,tag:'v5.0.0',candidateCommit:'main',remote:root}), /完整/);
  git('tag','-fa','v5.0.0','-m','release',newer);
  assert.equal(createSnapshot({sourceRoot:root,tag:'v5.0.0',remote:root}).profiles.spec.templateCommit,newer);
});
