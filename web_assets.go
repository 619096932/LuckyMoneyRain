package hongbao

import "embed"

//go:embed web/index.html web/admin.html web/wallet.html web/withdraw.html
var EmbeddedPages embed.FS
