package cmd

import (
	"net/http"
	"os"

	"github.com/openshift/online/archivist/pkg/api"

	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(archiverCmd)
}

var archiverCmd = &cobra.Command{
	Use:   "archiver",
	Short: "REST API to handle requests to archive and transfer projects.",
	Long:  `A REST API which will accept archival transfer requests and handle the transition and cleanup of a project to S3, or vice versa.`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetOutput(os.Stdout)
		//archivistCfg := loadConfig(cfgFile)

		_, oc, kc, err := createClients()
		if err != nil {
			log.Panicf("error creating OpenShift/Kubernetes clients: %s", err)
		}

		th := api.TransferHandler{oc, kc}

		router := mux.NewRouter().StrictSlash(true)
		router.HandleFunc("/api/transfer", th.Handle)
		log.Infoln("Starting archiver REST API.")
		log.Fatal(http.ListenAndServe(":10000", router))
	},
}
