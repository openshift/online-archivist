package cmd

import (
	"net/http"
	"os"

	"github.com/openshift/online/archivist/pkg/api"
	authclientset "github.com/openshift/origin/pkg/authorization/generated/clientset"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"
	userclientset "github.com/openshift/origin/pkg/user/generated/clientset"

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
		loadConfig(cfgFile)

		restConfig, factory, oc, kc, err := createClients()
		if err != nil {
			log.Panicf("error creating OpenShift/Kubernetes clients: %s", err)
		}

		projectClient, err := projectclientset.NewForConfig(restConfig)
		if err != nil {
			log.Panicf("error creating project client")
		}

		authClient, err := authclientset.NewForConfig(restConfig)
		if err != nil {
			log.Panicf("error creating auth client")
		}

		userClient, err := userclientset.NewForConfig(restConfig)
		if err != nil {
			log.Panicf("error creating user client")
		}

		th := api.NewTransferHandler(projectClient, authClient, userClient,
			oc.UserIdentityMappings(), oc.Identities(), factory, oc, kc)

		router := mux.NewRouter().StrictSlash(true)
		router.HandleFunc("/api/transfer", th.Handle)
		log.Infoln("Starting archiver REST API.")
		log.Fatal(http.ListenAndServe(":10000", router))
	},
}
