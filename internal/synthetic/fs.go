package synthetic

import (
	"os"
	"path/filepath"
)

var (
	osMkdirTemp  = os.MkdirTemp
	filepathBase = filepath.Base
)
