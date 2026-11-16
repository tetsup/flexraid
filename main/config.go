package main

import (
	"github.com/tetsup/flexraid/management"
	"github.com/tetsup/flexraid/pkg/shared/config"
)

const minimumDiskCount = config.DefaultDiskCount

type mountConfig = management.Config

// loadConfig delegates startup configuration loading to Management.
func loadConfig(path string) (mountConfig, error) {
	return management.LoadConfig(path)
}
