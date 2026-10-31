//go:build !yss_nobundle

package bundle

import _ "embed"

//go:embed assets/bundles.json.gz
var packedData []byte
