package config

import "runtime"

func ReportRuntimePlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func IsPathCaseInsensitive() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}
