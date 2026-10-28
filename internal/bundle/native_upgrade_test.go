package bundle

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestProducedNativeMetadataConsumer(t *testing.T) {
	raw := map[string]sourceFile{"scripts/lib/instance-metadata.mjs": {data: []byte("old source"), mode: 0644}}
	transforms, err := nativeUpgradeSource(raw, 4)
	if err != nil || len(transforms) != 1 {
		t.Fatalf("transform: %v %v", transforms, err)
	}
	root := t.TempDir()
	files := map[string][]byte{
		"instance-metadata.mjs": raw["scripts/lib/instance-metadata.mjs"].data,
		"validation-phase.mjs":  []byte(`export {existsSync,lstatSync,readFileSync} from 'node:fs';`),
		"structured-assets.mjs": []byte(`export const parseAsset = bytes => JSON.parse(bytes.toString());`),
		"probe.mjs": []byte(`import assert from 'node:assert/strict';
import {writeFileSync,rmSync} from 'node:fs';
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
check({schemaVersion:5,bundleSchemaVersion:5,distribution:{installation:"skills"}},true);
check({schemaVersion:5,bundleSchemaVersion:5,distribution:{installation:"governed"}},true);
for(const schemaVersion of [3,4])check({schemaVersion,bundleSchemaVersion:schemaVersion,distribution:{installation:'skills'}},false);
check({profile:'design',profileId:'harness.business-ddd-strategy-handoff'},false);
check({baselineDigest:'c'.repeat(64)},false);
const binding={schema_version:2,plugin:'yss-backend-delivery',profile:'spec',template_commit:commit,bundle_hash:hash,binary_sha256:hash};
const bindingRef='.yss-backend-plugin.json';
writeFileSync(bindingRef,JSON.stringify(binding));
for(const schemaVersion of [2,3,4,5])check({schemaVersion,bundleSchemaVersion:schemaVersion,distribution:schemaVersion===5?{installation:'governed'}:{}},true);
check({schemaVersion:5,bundleSchemaVersion:5,distribution:{installation:'skills'}},false);
for(const [key,value] of [['schema_version',3],['plugin','unknown'],['profile','backend'],['template_commit','c'.repeat(40)],['bundle_hash','c'.repeat(64)],['binary_sha256','invalid']]){
 writeFileSync(bindingRef,JSON.stringify({...binding,[key]:value}));
 check({},false);
}
rmSync(bindingRef);
const wbRecord={baseline:{type:'file',digest:hash,mode:420},lastApplied:{type:'file',digest:hash,mode:420},ownership:'managed'};
const wbMetadata={distribution:{runtimes:['workbuddy'],installedSkills:['a']},managedFiles:{'.agents/skills/a/SKILL.md':wbRecord,'.workbuddy/skills/a/SKILL.md':wbRecord}};
const wbLock={version:3,canonicalRoot:'.agents/skills',projectionRoots:['.workbuddy/skills'],skills:{shared:{a:{targets:['.agents/skills','.workbuddy/skills']}},platform:{}}};
assert.equal(nativeInstalledSkillRefs(wbMetadata,wbLock).length,2);
const oldRoot=value=>JSON.parse(JSON.stringify(value).replaceAll('.workbuddy/skills','.codebuddy/skills'));
assert.throws(()=>nativeInstalledSkillRefs(oldRoot(wbMetadata),oldRoot(wbLock)),/INSTANCE_DISTRIBUTION/);
assert.throws(()=>nativeInstalledSkillRefs({...wbMetadata,distribution:{runtimes:[],installedSkills:['a']}},wbLock),/INSTANCE_DISTRIBUTION/);
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

func TestNativeUpgradePreservesBundle4HarnessGuidance(t *testing.T) {
	const guidance = "current template guidance\n"
	raw := map[string]sourceFile{
		".template-spec/process/harness-upgrade.md": {data: []byte(guidance), mode: 0640},
		"scripts/lib/instance-metadata.mjs":         {data: []byte("old metadata consumer"), mode: 0600},
	}
	for _, root := range []string{".agents", ".codex", ".cursor", ".pi", ".claude"} {
		for _, ref := range []string{
			"setup-yss-harness/SKILL.md", "setup-yss-harness/references/project-operations.md", "setup-yss-harness/references/operation-contract.md",
			"yss-harness-upgrade/SKILL.md", "yss-harness-upgrade/references/project-operations.md",
		} {
			raw[root+"/skills/"+ref] = sourceFile{data: []byte(guidance), mode: 0640}
		}
	}
	transforms, err := nativeUpgradeSource(raw, 4)
	if err != nil {
		t.Fatal(err)
	}
	for ref, file := range raw {
		if ref == "scripts/lib/instance-metadata.mjs" {
			continue
		}
		if !bytes.Equal(file.data, []byte(guidance)) || file.mode != 0640 {
			t.Fatalf("Bundle4 guidance changed: %s", ref)
		}
	}
	if len(transforms) != 1 || transforms[0].Path != "scripts/lib/instance-metadata.mjs" {
		t.Fatalf("only metadata consumer should be transformed: %v", transforms)
	}
	original, err := transforms[0].Source.Render(nil)
	if err != nil || string(original) != "old metadata consumer" || transforms[0].Source.Mode != 0600 {
		t.Fatalf("metadata source not preserved: %s %v", original, err)
	}
	if bytes.Equal(raw["scripts/lib/instance-metadata.mjs"].data, original) || raw["scripts/lib/instance-metadata.mjs"].mode != 0600 {
		t.Fatal("metadata consumer was not transformed with its original mode")
	}
}

func TestNativeUpgradeKeepsHistoricalHarnessCompatibility(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			for _, renamed := range []bool{false, true} {
				inputs := map[string]string{".template-spec/process/harness-upgrade.md": "harness-upgrade.md"}
				for _, root := range []string{".agents", ".codex", ".cursor", ".pi", ".claude"} {
					inputs[root+"/skills/yss-harness-upgrade/SKILL.md"] = "upgrade-SKILL.md"
					inputs[root+"/skills/yss-harness-upgrade/references/project-operations.md"] = "project-operations.md"
					if renamed {
						inputs[root+"/skills/setup-yss-harness/SKILL.md"] = "setup-SKILL.md"
						inputs[root+"/skills/setup-yss-harness/references/project-operations.md"] = "setup-project-operations.md"
						inputs[root+"/skills/setup-yss-harness/references/operation-contract.md"] = "setup-operation-contract.md"
					}
				}
				if renamed {
					inputs[".template-spec/process/harness-upgrade.md"] = "setup-contract.md"
				}
				raw := map[string]sourceFile{}
				for ref := range inputs {
					raw[ref] = sourceFile{data: []byte("historical original\n"), mode: 0640}
				}
				transforms, err := nativeUpgradeSource(raw, version)
				if err != nil {
					t.Fatal(err)
				}
				if len(transforms) != len(inputs) {
					t.Fatalf("missing historical transforms: %d", len(transforms))
				}
				for _, transform := range transforms {
					expected, err := nativeUpgradeAssets.ReadFile("native/" + inputs[transform.Path])
					if err != nil {
						t.Fatal(err)
					}
					file := raw[transform.Path]
					if !bytes.Equal(file.data, expected) || file.mode != 0640 {
						t.Fatalf("historical output changed: %s", transform.Path)
					}
					original, err := transform.Source.Render(nil)
					if err != nil || string(original) != "historical original\n" || transform.Source.Mode != 0640 {
						t.Fatalf("historical source lost: %s %v", transform.Path, err)
					}
				}
			}
		})
	}
}
