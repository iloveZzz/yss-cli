//go:build !yss_nobundle

package bundle

import "embed"

// 嵌入归档由 tools/fetch-bundle.mjs 按 YSS_SOURCE_SNAPSHOT 生成，不进入版本库。
//
//go:embed assets/bundles.json.gz
var packedData []byte

//go:embed assets/trusted/*
var trustedSources embed.FS
