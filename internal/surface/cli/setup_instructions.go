package cli

import (
	"github.com/dev-toolings/ghostchrome/internal/setup"
	"github.com/spf13/cobra"
)

func init() {
	instructionsCmd := &cobra.Command{
		Use:   "instructions",
		Short: "Add the managed Ghostchrome policy to global agent instructions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := setup.RunInstructions(cmd.OutOrStdout(), setupInstructionsWrite, setupClientsFlag); err != nil {
				return setupCommandError("instructions", err)
			}
			return nil
		},
	}
	instructionsCmd.Flags().BoolVar(&setupInstructionsWrite, "write", false, "Confirm writing managed instruction blocks")
	instructionsCmd.Flags().StringVar(&setupClientsFlag, "clients", setup.DefaultClients, "Comma-separated global clients: claude,codex,grok")
	setupCmd.AddCommand(instructionsCmd)
}
