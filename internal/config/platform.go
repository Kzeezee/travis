package config

import "runtime"

func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
