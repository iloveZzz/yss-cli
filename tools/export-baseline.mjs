#!/usr/bin/env node
// Historical entry point retained only to explain the native producer command.
// It never imports or executes the retired CLI packages.
console.error('Bundle production moved to Go: go run ./tools/bundle --source-root <fixed-template-root> --lock docs/source-lock.json --out internal/bundle/assets');
process.exitCode = 1;
