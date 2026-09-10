//go:build !unix

package golang

import "os"

func inodeOf(os.FileInfo) string { return "-" }
