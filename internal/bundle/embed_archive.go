//go:build !yss_nobundle

package bundle

import _ "embed"

// 嵌入归档由 tools/fetch-bundle.mjs 按 docs/source-lock.json 生成，不进入版本库。
//
//go:embed assets/bundles.json.gz
var packedData []byte
