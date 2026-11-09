//go:build official

package licensing

import _ "embed"

// Official builds embed the LibreKey public key(s); a valid license is required.
//
//go:embed librekey.pub
var officialKeys []byte
