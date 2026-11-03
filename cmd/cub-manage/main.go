// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Command cub-manage manages Kubernetes configuration stored as data in ConfigHub
// across a fleet: namespaces, RBAC, NetworkPolicy, workload readiness,
// scheduling, autoscaling, and observability, one command group each.
//
// It runs standalone (cub-manage ...) or as the cub plugin command
// 'cub manage ...'. All ConfigHub I/O goes through the ConfigHub API, via the
// github.com/confighub/sdk/core/cubapi package, using the ambient cub session.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/confighub/sdk/core/plugin"

	"github.com/confighub/cub-manage/internal/cli"
	"github.com/confighub/cub-manage/internal/version"
)

func main() {
	// When cub installs or upgrades this plugin it invokes the binary as a hook;
	// HandleHook writes cub-plugin.yaml into the plugin directory and we exit
	// without running the command tree.
	manifest := plugin.Manifest{
		Name:    "cub-manage",
		Version: version.Version,
		Commands: []plugin.Command{{
			Name:    "manage",
			Summary: cli.Summary,
		}},
	}
	if handled, err := plugin.HandleHook(manifest); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// A command's output is its report. The SDK logs each function it runs
	// in-process at info level, which would be noise beside it.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	if err := cli.NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
