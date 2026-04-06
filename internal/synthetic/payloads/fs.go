package payloads

import "os"

var (
	osReadDir  = os.ReadDir
	osReadFile = os.ReadFile
)
