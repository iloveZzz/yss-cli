package bundle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestProducedNativeMetadataConsumer(t *testing.T) {
	raw := map[string]sourceFile{"scripts/lib/instance-metadata.mjs": {data: []byte("old source"), mode: 0644}}
	transforms, err := nativeUpgradeSource(raw)
	if err != nil || len(transforms) != 1 {
		t.Fatalf("transform: %v %v", transforms, err)
	}
	root := t.TempDir()
	files := map[string][]byte{
		"instance-metadata.mjs": raw["scripts/lib/instance-metadata.mjs"].data,
		"validation-phase.mjs":  []byte(`export {existsSync,lstatSync,readFileSync} from 'node:fs';`),
		"structured-assets.mjs": []byte(`export const parseAsset = bytes => JSON.parse(bytes.toString());`),
		"probe.mjs": []byte(`import assert from 'node:assert/strict';
import {writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {readInstanceMetadata,nativeInstalledSkillRefs} from './instance-metadata.mjs';
const hash='a'.repeat(64), commit='b'.repeat(40);
const base={schemaVersion:4,bundleSchemaVersion:4,protocolVersion:1,profile:'spec',profileId:'harness.spec-template',templateSourceState:'committed',templateCommit:commit,snapshotHash:hash,manifestHash:hash,bundleHash:hash,cliSourceState:'committed',cliCommit:commit,variables:{},distribution:{},managedFiles:{},baselineDigest:createHash('sha256').update('{}').digest('hex')};
const check=(patch,valid)=>{writeFileSync('.yss.json',JSON.stringify({...base,...patch}));if(valid)assert.equal(readInstanceMetadata('.').kind,'native');else assert.throws(()=>readInstanceMetadata('.'),/INSTANCE_IDENTITY/);};
check({},true);
check({schemaVersion:3,bundleSchemaVersion:3},true);
check({profile:'backend',profileId:'harness.backend-delivery'},true);
check({bundleSchemaVersion:3},false);
check({schemaVersion:5,bundleSchemaVersion:5},false);
check({profile:'design',profileId:'harness.business-ddd-strategy-handoff'},false);
check({baselineDigest:'c'.repeat(64)},false);
for(const version of [3,4])assert.deepEqual(nativeInstalledSkillRefs(base,{version,canonicalRoot:'.agents/skills',skills:{shared:{},platform:{}},projectionRoots:[]}),[]);
assert.throws(()=>nativeInstalledSkillRefs(base,{version:4,canonicalRoot:'.agents/skills',skills:{shared:{},platform:{}},projectionRoots:['.codex/skills']}),/INSTANCE_DISTRIBUTION/);
`),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "probe.mjs")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("produced consumer: %v\n%s", err, out)
	}
}
