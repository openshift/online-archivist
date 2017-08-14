package cmd

import (
	"github.com/spf13/cobra"
)

var RootCmd = &cobra.Command{
	Use:   "archivist",
	Short: "Microservices to manage archival of users in an OpenShift clusters.",
	Run: func(cmd *cobra.Command, args []string) {
		// Do Stuff Here
	},
}

var cfgFile string

func init() {
	RootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "load configuration from file")
}

func Execute() {
	RootCmd.Execute()
}
