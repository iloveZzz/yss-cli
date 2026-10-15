//go:build yss_nobundle

package bundle

// 仅用于 tools/bundle 在归档尚不存在时自举编译：go run -tags yss_nobundle ./tools/bundle。
// 带此 tag 的二进制不含任何模板资产，不得作为 CLI 发行。
var packedData []byte
