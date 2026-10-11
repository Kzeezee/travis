package config

import "runtime"

func ReportRuntimePlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// ReportNativeLineEnding is the line break fs_write uses for new files.
func ReportNativeLineEnding() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

func IsPathCaseInsensitive() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}
