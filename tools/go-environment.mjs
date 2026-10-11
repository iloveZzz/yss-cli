// Keep implicit Go overlays, workspaces and tuning out of recorded builds.
import path from 'node:path';
import {execFileSync} from 'node:child_process';

export const GO_ENV_KEYS = ['GOVERSION','GOOS','GOARCH','GOAMD64','GOARM','GOARM64','GO386','GOMIPS','GOMIPS64','GOPPC64','GORISCV64','GOWASM','GOEXPERIMENT','CGO_ENABLED','GOFLAGS','GOWORK','GOTOOLCHAIN','GOENV','GO111MODULE'];

export function fixedGoEnvironment() {
  const env = {...process.env,CGO_ENABLED:'0',GOENV:'off',GOWORK:'off',GOFLAGS:'',GOEXPERIMENT:'',GOTOOLCHAIN:'local',GO111MODULE:'on'};
  for (const key of ['GOAMD64','GOARM','GOARM64','GO386','GOMIPS','GOMIPS64','GOPPC64','GORISCV64','GOWASM']) delete env[key];
  return env;
}

export function checkModuleInputs(root,env) {
  const mod = JSON.parse(execFileSync('go',['mod','edit','-json'],{cwd:root,env,encoding:'utf8'}));
  if ((mod.Replace ?? []).some(replacement=>!replacement.New.Version)) throw new Error('local module replacements are outside the fixed build contract');
}

export function checkPackageInputs(root,env,inputs,targets=['./cmd/yss']) {
  const fields=['GoFiles','SFiles','HFiles','SysoFiles','EmbedFiles'];
  const template='{{if and .Module .Module.Main}}{{.Dir}}'+fields.map(field=>'{{range .'+field+'}}{{"\\t"}}{{.}}{{end}}').join('')+'{{end}}';
  const listed=execFileSync('go',['list','-mod=readonly','-deps','-f',template,...targets],{cwd:root,env,encoding:'utf8'});
  for(const row of listed.split('\n').filter(Boolean)) {
    const [directory,...files]=row.split('\t');
    for(const file of files) {
      const ref=path.relative(root,path.join(directory,file)).split(path.sep).join('/');
      if(!inputs[ref]) throw new Error(`compiled or embedded input missing from inventory: ${ref}`);
    }
  }
}
