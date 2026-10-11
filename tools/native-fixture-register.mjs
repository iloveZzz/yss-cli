/** Adapt synthetic evidence to the native seed whose unchanged readers consume it.
 * The pinned upstream fixture defaults to its own template checkout. Production
 * modules and installed assets are never rewritten; only this fixture producer
 * receives the explicit source root it already accepts as nativeSeed.
 */
import { registerHooks } from "node:module";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

export function bindNativeFixtureSource(source) {
  const old = "source_fingerprint:platformSourceFingerprint(family)";
  const bound = "source_fingerprint:platformSourceFingerprint(family,nativeSeed || undefined)";
  if (source.includes(bound)) return source;
  if (source.split(old).length !== 2) throw new Error("fixed native fixture source changed; review the source-root adapter");
  return source.replace(old, bound);
}

if (process.env.YSS_LEGACY_ORACLE_ROOT) {
  // Node resolves module URLs through symlinks (including macOS /var -> /private/var).
  // Keep the exact-file boundary after applying the same canonicalization.
  const fixtureURL = pathToFileURL(fs.realpathSync(path.join(process.env.YSS_LEGACY_ORACLE_ROOT, "scripts/fixtures/existing-backend/fixture.mjs"))).href;
  registerHooks({
    load(url, context, nextLoad) {
      const loaded = nextLoad(url, context);
      if (url !== fixtureURL) return loaded;
      return { ...loaded, source: bindNativeFixtureSource(String(loaded.source)) };
    },
  });
}
