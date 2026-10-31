#!/usr/bin/env node
// Resolve a release tag once into a build-local compatibility snapshot.
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

const cliRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const remoteURL = 'https://github.com/iloveZzz/yss-spec-project-template.git';
const commitPattern = /^[a-f0-9]{40}$/;
const tagPattern = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const git = (cwd,args,encoding='utf8') => execFileSync('git',args,{cwd,encoding,stdio:['ignore','pipe','inherit']});
const digest = bytes => createHash('sha256').update(bytes).digest('hex');

// Historical descriptions are product facts, not version selectors.
const legacy = {
  spec: {version:'3.5.10',cliCommit:'afc1b245f1957b8f6a041bdee9a75c871f37a515',sourceSnapshotHash:'a2ef1c2c14fa197d069e76484148d28c01d24f242cf5b118b7ef234df2b76848',manifestHash:'544e8ae01c49ef451a784237b06772137876f8e8d6b5334094827e2ca2fe0f13'},
  backend: {version:'0.4.21',cliCommit:'0799872b082fe7decea59db7eb6545692472d2d2',sourceSnapshotHash:'e53990d44b3de929d8c3c893fdd8670ca19a0180fbd785c59a90580b32d702c7',manifestHash:'188f3c2a97dd2c3a44e3be4a98d3125fbe7ae8e50426cfc7dd769af4d42f2ce6'}
};

export function createSnapshot({sourceRoot, tag, candidateCommit, remote = remoteURL}) {
  if (!tagPattern.test(tag)) throw new Error('需要稳定模板版本 tag');
  sourceRoot = fs.realpathSync(sourceRoot);
  const top = fs.realpathSync(git(sourceRoot,['rev-parse','--show-toplevel']).trim());
  if (top !== sourceRoot) throw new Error('模板来源必须是 Git 仓库根目录');
  let commit = candidateCommit;
  if (candidateCommit !== undefined) {
    if (!commitPattern.test(commit)) throw new Error('候选需要完整提交');
  } else {
    const ref = `refs/tags/${tag}`;
    const rows = git(sourceRoot,['ls-remote','--exit-code',remote,ref,`${ref}^{}`]).trim().split('\n');
    const refs = new Map(rows.map(row => row.split(/\s+/).reverse()));
    commit = refs.get(`${ref}^{}`) ?? refs.get(ref);
    if (!commitPattern.test(commit || '')) throw new Error('远端模板 tag 未解析为提交');
    const local = git(sourceRoot,['rev-parse','--verify',`${ref}^{commit}`]).trim();
    if (local !== commit) throw new Error('模板仓的 tag 与远端不一致');
  }
  if (git(sourceRoot,['cat-file','-t',commit]).trim() !== 'commit') throw new Error('模板来源不是提交');
  const producerPaths = ['internal/bundle','tools/bundle',':(exclude)internal/bundle/assets',':(exclude)**/*.md'];
  if (git(cliRoot,['status','--porcelain','--untracked-files=all','--',...producerPaths]).trim()) throw new Error('Bundle 生成器源码必须已提交');
  const producerCommit = git(cliRoot,['log','-1','--format=%H','--',...producerPaths]).trim();
  if (!commitPattern.test(producerCommit)) throw new Error('无法确定已提交的 Bundle 生成器');
  const profiles = {};
  for (const name of ['spec','backend']) {
    const policyPath = name === 'spec' ? '.template-source/distribution/bundle-profile.json' : 'profiles/backend/.template-source/distribution/bundle-profile.json';
    const policyHash = digest(git(sourceRoot,['show',`${commit}:${policyPath}`],null));
    profiles[name] = {profile:name,sourcePath:'.',templateCommit:commit,templateVersion:tag.slice(1),...(candidateCommit === undefined ? {templateTag:tag} : {}),policyPath,policyHash,...(name === 'backend' ? {overlayPaths:['profiles/_specialist','profiles/backend']} : {}),legacy:legacy[name],sourceState:'committed'};
  }
  return {schemaVersion:5,producer:{version:'bundle-producer-v4',commit:producerCommit,sourceState:'committed'},profiles};
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = {};
    const args = process.argv.slice(2);
    for (let i=0;i<args.length;i+=2) {
      if (!['--source-root','--out','--candidate-commit'].includes(args[i]) || !args[i+1] || Object.hasOwn(options,args[i])) throw new Error('usage: resolve-template.mjs --source-root DIR --out FILE [--candidate-commit SHA]');
      options[args[i]] = args[i+1];
    }
    if (!options['--source-root'] || !options['--out'] || !path.isAbsolute(options['--out'])) throw new Error('需要模板仓和仓外绝对输出路径');
    const requested = path.resolve(options['--out']);
    const out = path.join(fs.realpathSync(path.dirname(requested)),path.basename(requested)), relative = path.relative(fs.realpathSync(cliRoot),out);
    if (!relative || (!relative.startsWith('..'+path.sep) && relative !== '..' && !path.isAbsolute(relative))) throw new Error('快照输出必须在 CLI 仓外');
    const {tag} = JSON.parse(fs.readFileSync(path.join(cliRoot,'docs/template-release.json'),'utf8'));
    const snapshot = createSnapshot({sourceRoot:options['--source-root'],tag,candidateCommit:options['--candidate-commit']});
    fs.writeFileSync(out,JSON.stringify(snapshot,null,2)+'\n',{flag:'wx'});
    console.log(JSON.stringify({status:'resolved',sourceMode:options['--candidate-commit'] ? 'pr-candidate' : 'release-tag',commit:snapshot.profiles.spec.templateCommit,out}));
  } catch (error) {console.error(error.message); process.exitCode = 1}
}
