package api

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/openshift/online/archivist/pkg/model"

	log "github.com/Sirupsen/logrus"
	osclient "github.com/openshift/origin/pkg/client"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
)

// TransferHandler is a struct carrying objects we need to use to process each API request.
type TransferHandler struct {
	OpenshiftClient osclient.Interface
	KubeClient      kclientset.Interface
}

// TransferHandler handles all transfer API requests.
func (th TransferHandler) Handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		httpStatus, err := th.initiateTransfer(r)
		if err != nil {
			http.Error(w, err.Error(), httpStatus)
		}
	default:
		log.Errorln("unhandled HTTP method:", r.Method)
		http.Error(w, "unsupported request", http.StatusBadRequest)
	}
}

func (th TransferHandler) initiateTransfer(r *http.Request) (httpStatus int, err error) {
	reqLog := log.WithFields(log.Fields{
		"method": r.Method,
	})
	reqLog.Infoln("Handling request: ", r)
	var t model.Transfer
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return http.StatusBadRequest, err
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return http.StatusBadRequest, err
	}
	reqLog.Infoln("parsed transfer", t)
	return http.StatusAccepted, nil
}
