// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/cub"
)

// newPreflightCmd checks that cub-manage can talk to ConfigHub: the client can be
// built from the ambient session and that session is valid against the server.
// Every ConfigHub-touching command runs the same check first; this exposes it on
// its own so an agent can verify readiness before attempting real work.
func newPreflightCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "preflight",
		Short: "Verify the ConfigHub session is valid",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := cub.Preflight(cmd.Context()); err != nil {
				return err
			}
			cmd.Println("cub-manage: ready (ConfigHub session valid)")
			return nil
		},
	}
}
