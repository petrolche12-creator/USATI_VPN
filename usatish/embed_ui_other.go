//go:build !windows

package main

import "embed"

// uiFS — страница локального интерфейса для браузера. Используется только на Linux
// (main_other.go и http.go); в Windows-сборку не попадает.
//
//go:embed ui
var uiFS embed.FS
