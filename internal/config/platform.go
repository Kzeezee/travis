package config

import "runtime"

func ReportRuntimePlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
