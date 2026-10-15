#!/usr/bin/env node
// Fixed-source generation. Receipts are evidence, never a new source authority.
import fs from 'node:fs';
import {fixedGoEnvironment,GO_ENV_KEYS,checkModuleInputs,checkPackageInputs} from './go-environment.mjs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

const PROFILES = ['spec', 'design', 'backend'];
const ARCHIVE = 'internal/bundle/assets/bundles.json.gz';
const DIGEST = ARCHIVE + '.sha256'; // 入库的归档摘要；归档本身不入库，由 tools/fetch-bundle.mjs 生成
const VIEWS = PROFILES.map(p => `internal/helpview/assets/${p}.json`);
const HELP = 'docs/cli-help.md';
const TARGETS = [ARCHIVE, DIGEST, ...VIEWS, HELP];
const sha = value => createHash('sha256').update(value).digest('hex');
const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

function identity(file) {
  try {
    const stat = fs.lstatSync(file);
    if (!stat.isFile() && !stat.isSymbolicLink()) throw new Error(`not a file: ${file}`);
    return {sha256: sha(stat.isSymbolicLink() ? fs.readlinkSync(file) : fs.readFileSync(file)), mode: stat.mode & 0o777, link: stat.isSymbolicLink()};
  } catch (error) {if (error.code === 'ENOENT') return null; throw error}
}
const equal = (a,b) => JSON.stringify(a) === JSON.stringify(b);
const digestLine = hex => `${hex}  bundles.json.gz\n`;

export function runArtifacts({root = repositoryRoot, mode, scope, sourceRoot, out}) {
  if (!['check','generate'].includes(mode) || !['derived','all'].includes(scope)) throw new Error('usage: artifacts.mjs check|generate --scope derived|all --out DIR [--source-root ROOT]');
  if (scope === 'all' && !sourceRoot) throw new Error('--scope all requires --source-root');
  if (!out) throw new Error('--out requires a new evidence directory outside the repository');
  root = fs.realpathSync(root);
  out = path.resolve(out);
  fs.mkdirSync(path.dirname(out), {recursive:true});
  out = path.join(fs.realpathSync(path.dirname(out)), path.basename(out));
  const relative = path.relative(root,out);
  if (!relative || (!relative.startsWith('..'+path.sep) && relative !== '..' && !path.isAbsolute(relative))) throw new Error('--out must be outside the repository');
  fs.mkdirSync(out); // Never reuse or overwrite another run's evidence.
  const receipt = {schemaVersion:1, kind:'generated-artifacts', mode, scope, status:'running', root, createdAt:new Date().toISOString(), commands:[], sources:[], outputs:{}, published:[]};
  const env = fixedGoEnvironment();
  const receiptFile = path.join(out,'receipt.json');
  const save = () => fs.writeFileSync(receiptFile, JSON.stringify(receipt,null,2)+'\n');
  let lockPath, ownsLock = false, placeholder = false;
  const previousUmask = process.umask(0o022);
  receipt.umask = '0022';
  function run(command,args,cwd=root) {
    const index = receipt.commands.length;
    const record = {command,args,cwd,status:'running'};
    receipt.commands.push(record); save();
    const result = spawnSync(command,args,{cwd,env,encoding:'utf8',maxBuffer:64*1024*1024});
    fs.writeFileSync(path.join(out,`${index}.stdout.log`),result.stdout ?? '');
    fs.writeFileSync(path.join(out,`${index}.stderr.log`),result.stderr ?? '');
    record.exitCode = result.status; record.signal = result.signal;
    record.status = !result.error && result.status === 0 ? 'passed' : 'failed'; save();
    if (record.status !== 'passed') throw result.error ?? new Error(`${command} ${args.join(' ')} failed: ${(result.stderr ?? '').trim()}`);
    return result.stdout.trim();
  }
  const git = args => run('git',args);
  function inputs() {
    // Include uncommitted generator inputs. The known outputs are compared separately.
    const result = spawnSync('git',['ls-files','--cached','--others','--exclude-standard','-z'],{cwd:root,encoding:'utf8'});
    if (result.status !== 0) throw new Error('cannot inventory generator inputs');
    return Object.fromEntries([...new Set(result.stdout.split('\0').filter(Boolean))].sort().filter(ref=>!TARGETS.includes(ref)).map(ref=>[ref,identity(path.join(root,ref))]));
  }
  try {
    save();
    if (mode === 'generate') {
      lockPath = path.resolve(root,git(['rev-parse','--git-path','yss-artifacts.lock']));
      const fd = fs.openSync(lockPath,'wx',0o600);
      ownsLock = true;
      try {fs.writeFileSync(fd,JSON.stringify({pid:process.pid,out,createdAt:receipt.createdAt})+'\n')} finally {fs.closeSync(fd)}
    }
    checkModuleInputs(root,env);
    receipt.goEnvironment = JSON.parse(run('go',['env','-json',...GO_ENV_KEYS]));
    receipt.goVersion = run('go',['version']); receipt.nodeVersion = process.version;
    const before = inputs(); receipt.inputDigest = sha(JSON.stringify(before));
    receipt.inputs = before;
    if (Object.values(before).some(value=>value?.link)) throw new Error('generator inputs must be ordinary files');
    const archivePath = path.join(root,ARCHIVE);
    const committedDigest = fs.readFileSync(path.join(root,DIGEST),'utf8').trim().split(/\s+/)[0];
    if (!/^[a-f0-9]{64}$/.test(committedDigest)) throw new Error(`摘要文件格式错误: ${DIGEST}`);
    if (scope === 'derived') {
      if (!fs.existsSync(archivePath) || sha(fs.readFileSync(archivePath)) !== committedDigest) throw new Error('内嵌归档缺失或与摘要不一致，请先运行 node tools/fetch-bundle.mjs');
    } else if (!fs.existsSync(archivePath)) {
      // 归档不入库：缺失时放空占位，仅让生成工具与 go list 能编译；结束时移除。
      fs.writeFileSync(archivePath,'',{flag:'wx'}); placeholder = true;
    }
    const generatorPackages = ['./tools/helpview','./tools/helpdocs','./tools/artifact-sizes'];
    if (scope === 'all') generatorPackages.push('./tools/bundle');
    const prior = Object.fromEntries(TARGETS.map(ref=>[ref,identity(path.join(root,ref))]));
    if (Object.values(prior).some(value=>value?.link)) throw new Error('generated targets must be ordinary files');
    checkPackageInputs(root,env,{...before,...prior},generatorPackages);
    const expectedTargets = {...prior};
    const checkTargets = () => {
      for (const ref of TARGETS) {
        if (!equal(expectedTargets[ref],identity(path.join(root,ref)))) throw new Error(`generated target changed concurrently: ${ref}`);
      }
    };
    const stage = path.join(out,'stage'); fs.mkdirSync(stage);
    const overlay = {Replace:{}};
    const overlayPath = path.join(out,'overlay.json');
    const candidates = {};
    if (scope === 'all') {
      sourceRoot = fs.realpathSync(sourceRoot);
      const lock = JSON.parse(fs.readFileSync(path.join(root,'docs/source-lock.json'),'utf8'));
      for (const [profile,source] of Object.entries(lock.profiles)) {
        for (const item of [source,source.skillsSource].filter(Boolean)) {
          if (!/^[a-f0-9]{40}$/.test(item.templateCommit)) throw new Error(`fixed source commit required: ${profile}`);
          const repo = fs.realpathSync(path.resolve(sourceRoot,item.sourcePath));
          if (path.relative(sourceRoot,repo).split(path.sep).includes('..')) throw new Error('sourcePath escapes source-root');
          const top = fs.realpathSync(run('git',['rev-parse','--show-toplevel'],repo));
          if (top !== repo) throw new Error(`source repository not initialized: ${repo}`);
          run('git',['cat-file','-e',item.templateCommit+'^{commit}'],repo);
          receipt.sources.push({profile,root:repo,commit:item.templateCommit});
        }
      }
      const archiveDir = path.join(stage,'bundle');
      run('go',['run','./tools/bundle','--source-root',sourceRoot,'--lock','docs/source-lock.json','--out',archiveDir]);
      candidates[ARCHIVE] = path.join(archiveDir,'bundles.json.gz');
      overlay.Replace[path.join(root,ARCHIVE)] = candidates[ARCHIVE];
      candidates[DIGEST] = path.join(archiveDir,'bundles.json.gz.sha256');
      fs.writeFileSync(candidates[DIGEST],digestLine(sha(fs.readFileSync(candidates[ARCHIVE]))));
    }
    fs.writeFileSync(overlayPath,JSON.stringify(overlay));
    const viewDir = path.join(stage,'views');
    // Each fresh compiler sees the preceding candidate via overlay, including go:embed.
    run('go',['run','-overlay',overlayPath,'./tools/helpview','--out',viewDir]);
    for (let i=0;i<VIEWS.length;i++) {
      candidates[VIEWS[i]] = path.join(viewDir,PROFILES[i]+'.json');
      overlay.Replace[path.join(root,VIEWS[i])] = candidates[VIEWS[i]];
    }
    fs.writeFileSync(overlayPath,JSON.stringify(overlay));
    candidates[HELP] = path.join(stage,'cli-help.md');
    run('go',['run','-overlay',overlayPath,'./tools/helpdocs','--reference','--out',candidates[HELP]]);
    receipt.capacity = JSON.parse(run('go',['run','-overlay',overlayPath,'./tools/artifact-sizes']));
    for (const profile of PROFILES) {
      const size = receipt.capacity[profile]?.snapshotBytes;
      if (!Number.isSafeInteger(size) || size <= 0 || size > 128*1024*1024) throw new Error(`public snapshot exceeds 128 MiB or is missing: ${profile}`);
    }
    const archive = candidates[ARCHIVE] ?? path.join(root,ARCHIVE);
    receipt.archiveBytes = fs.statSync(archive).size;
    if (receipt.archiveBytes <= 0 || receipt.archiveBytes > 10_000_000) throw new Error('archive exceeds 10,000,000 bytes');
    if (!equal(before,inputs())) throw new Error('generator inputs changed during generation');
    checkTargets();
    const changed = [];
    for (const [ref,candidate] of Object.entries(candidates)) {
      const candidateIdentity = identity(candidate);
      receipt.outputs[ref] = {...candidateIdentity, bytes:fs.statSync(candidate).size, changed:prior[ref]?.sha256 !== candidateIdentity.sha256 || ((prior[ref]?.mode ?? 0) & 0o111) !== (candidateIdentity.mode & 0o111)};
      if (receipt.outputs[ref].changed) changed.push(ref);
    }
    // 归档只在本地存在，check 不比对它本身；它的内容由入库摘要（DIGEST）约束。
    const drift = mode === 'check' ? changed.filter(ref=>ref !== ARCHIVE) : [];
    if (drift.length) throw new Error('生成产物漂移: '+drift.join(', '));
    if (mode === 'generate') {
      for (const ref of Object.keys(candidates)) {
        const target = path.join(root,ref);
        if (!equal(prior[ref],identity(target))) throw new Error(`generated target changed concurrently: ${ref}`);
        if (!receipt.outputs[ref].changed) continue;
        const temporary = target+`.yss-artifacts-${process.pid}.tmp`;
        let ownsTemporary = false;
        try {
          const fd = fs.openSync(temporary,'wx',0o644);
          ownsTemporary = true;
          try {fs.writeFileSync(fd,fs.readFileSync(candidates[ref]))} finally {fs.closeSync(fd)}
          if (!equal(prior[ref],identity(target))) throw new Error(`generated target changed concurrently: ${ref}`);
          fs.renameSync(temporary,target);
          expectedTargets[ref] = identity(target);
        } finally {if(ownsTemporary && fs.existsSync(temporary)) fs.unlinkSync(temporary)}
        receipt.published.push(ref); save();
      }
      run('go',['run','./tools/helpview','--check']);
      run('go',['run','./tools/helpdocs','--reference','--check']);
    }
    if (!equal(before,inputs())) throw new Error('generator inputs changed during publication');
    checkTargets();
    checkPackageInputs(root,env,{...inputs(),...expectedTargets},generatorPackages);
    receipt.status = mode === 'check' ? 'checked' : 'generated'; save();
    return receipt;
  } catch (error) {
    receipt.status = 'failed'; receipt.error = error.message; save(); throw error;
  } finally {
    try {if (placeholder && fs.existsSync(path.join(root,ARCHIVE)) && fs.statSync(path.join(root,ARCHIVE)).size === 0) fs.unlinkSync(path.join(root,ARCHIVE))}
    finally {try {if (ownsLock) fs.unlinkSync(lockPath)} finally {process.umask(previousUmask)}}
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [mode,...args] = process.argv.slice(2), options = {mode};
    for(let i=0;i<args.length;i+=2) {
      if(!['--scope','--source-root','--out'].includes(args[i]) || !args[i+1]) throw new Error('unknown or incomplete artifact option');
      options[args[i]==='--source-root'?'sourceRoot':args[i].slice(2)] = args[i+1];
    }
    const receipt = runArtifacts(options);
    console.log(JSON.stringify({status:receipt.status,scope:receipt.scope,out:options.out,archiveBytes:receipt.archiveBytes}));
  } catch(error) {console.error(error.message);process.exitCode=1}
}
