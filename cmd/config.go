package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/akshatsinha007/kubuto/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage kubuto configuration",
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a default configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		configDir := filepath.Join(home, ".kubuto")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return err
		}

		configFile := filepath.Join(configDir, "config.yaml")
		if _, err := os.Stat(configFile); err == nil {
			fmt.Printf("Config file already exists: %s\n", configFile)
			return nil
		}

		defaultConfig := config.GetDefaultConfig()

		data, err := yaml.Marshal(defaultConfig)
		if err != nil {
			return err
		}

		if err := os.WriteFile(configFile, data, 0644); err != nil {
			return err
		}

		fmt.Printf("Default configuration file created: %s\n", configFile)
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display the current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Bug fix: this used to read viper.AllSettings() off the global
		// spf13/viper package singleton, which is never the instance
		// config.LoadConfig builds and populates (it always constructs its
		// own viper.New()) — so this command always printed {} regardless
		// of any real config file, env var, or default in effect. Load
		// through the same path every other command uses so this actually
		// reflects reality.
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Mask the API key before printing — this command is for verifying
		// what's configured (right engine URL? key actually set?), not for
		// putting a live secret into terminal scrollback, tmux history, or
		// a screen-shared session. Copy the struct so this only affects
		// what gets displayed, not the loaded config itself.
		display := *cfg
		display.Engine.APIKey = maskSecret(display.Engine.APIKey)

		data, err := yaml.Marshal(display)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	},
}

// maskSecret redacts all but a short prefix/suffix of a secret value so a
// user can still recognize which key is configured (e.g. to confirm it's
// the one they think it is) without the full value being readable wherever
// this command's output ends up.
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func init() {
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configShowCmd)
	rootCmd.AddCommand(configCmd)
}
