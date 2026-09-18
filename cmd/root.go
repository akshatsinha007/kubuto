package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var cfgFile string
var kubeconfig string

// Version is kubuto's own build version. It defaults to "dev" for a
// plain `go build`/`go run` and is overridden at release-build time via
// `-ldflags "-X github.com/akshatsinha007/kubuto/cmd.Version=..."` (see
// cmd/kubuto/main.go and .goreleaser.yml). Threaded into
// scanner.ClusterScanParams.Version so `kubuto scan`/`compat cluster`'s
// JSON output can report exactly what version produced it, and exposed
// as Cobra's built-in `--version` flag on the root command.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:     "kubuto",
	Short:   "A CLI tool to check for version updates in Kubernetes.",
	Version: Version,
	Long: `kubuto discovers deployed Helm, ArgoCD, and Flux resources across
your cluster and reports each one's current vs. latest upstream version.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.kubuto/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
}
