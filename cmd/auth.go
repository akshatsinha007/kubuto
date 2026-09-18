package cmd

import (
	"github.com/spf13/cobra"
)

// authCmd is the parent of `kubuto auth ...` subcommands. It exists to
// support `compat` (coming in v2) — there is no self-serve key issuance
// yet, and no `auth login` flow; a key is provisioned out-of-band and
// copied into `~/.kubuto/config.yaml`.
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication for compatibility checks (coming in v2)",
	Long: `Authentication subcommands, used by "kubuto compat" (coming in v2).

Requests are authenticated with an API key passed in the
'Authorization: Bearer <key>' header. There is no self-serve signup yet —
a key is provisioned for you out-of-band and configured via:

  ~/.kubuto/config.yaml
  engine:
    api_key: kbtu_xxxxxxxxxxxxxxxxx

Use 'kubuto auth validate' to confirm your key is recognized.`,
}

func init() {
	rootCmd.AddCommand(authCmd)
}
