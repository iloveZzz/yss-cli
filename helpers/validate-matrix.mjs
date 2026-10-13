import {parseArgs} from 'node:util';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
const {values}=parseArgs({options:{report:{type:'string'},sha256:{type:'string'},'binary-sha256':{type:'string'},'template-root':{type:'string'}},strict:true});
try {
  if(!values['template-root'])throw new Error('explicit fixed template root required');
  const {validateLegacyRecoveryMatrix}=await import(pathToFileURL(path.join(path.resolve(values['template-root']),'.template-source/scripts/lib/legacy-recovery-matrix.mjs')));
  const result=validateLegacyRecoveryMatrix({file:values.report,sha256:values.sha256,binarySha256:values['binary-sha256']});
  process.stdout.write(JSON.stringify({status:'passed',result})+'\n');
} catch(error) {process.stderr.write(String(error.message)+'\n');process.exitCode=1;}
