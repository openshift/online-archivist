package api

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/openshift/online/archivist/pkg/archive"
	"github.com/openshift/online/archivist/pkg/model"

	authclientset "github.com/openshift/origin/pkg/authorization/generated/clientset"
	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"
	userclientset "github.com/openshift/origin/pkg/user/generated/clientset"

	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/clientset"

	log "github.com/Sirupsen/logrus"
)

// TransferHandler is a struct carrying objects we need to use to process each API request.
type TransferHandler struct {
	projectClient projectclientset.Interface
	authClient    authclientset.Interface
	userClient    userclientset.Interface
	uidMapClient  osclient.UserIdentityMappingInterface
	idsClient     osclient.IdentityInterface

	oc osclient.Interface
	kc kclientset.Interface
	f  *clientcmd.Factory
}

func NewTransferHandler(
	projectClient projectclientset.Interface,
	authClient authclientset.Interface,
	userClient userclientset.Interface,
	uidMapClient osclient.UserIdentityMappingInterface,
	idsClient osclient.IdentityInterface,
	factory *clientcmd.Factory,
	oc osclient.Interface,
	kc kclientset.Interface) TransferHandler {

	return TransferHandler{
		projectClient: projectClient,
		authClient:    authClient,
		userClient:    userClient,
		uidMapClient:  uidMapClient,
		idsClient:     idsClient,
		oc:            oc,
		kc:            kc,
		f:             factory,
	}
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
	reqLog.Infoln("handling request: ", r)
	var t model.Transfer
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		reqLog.Errorln(err)
		return http.StatusBadRequest, err
	}
	if err := json.Unmarshal(body, &t); err != nil {
		reqLog.Errorln(err)
		return http.StatusBadRequest, err
	}
	reqLog.Infoln("parsed transfer", t)

	if t.Source.Cluster != nil {
		archiver := archive.NewArchiver(
			th.projectClient,
			th.authClient,
			th.userClient,
			th.uidMapClient,
			th.idsClient,
			th.f,
			th.oc,
			th.kc,
			t.Source.Cluster.Namespace,
			"admin")
		err := archiver.Archive()
		if err != nil {
			reqLog.Errorln(err)
			return http.StatusInternalServerError, err
		}
	}

	if t.Dest.Cluster != nil {
		archiver := archive.NewArchiver(
			th.projectClient,
			th.authClient,
			th.userClient,
			th.uidMapClient,
			th.idsClient,
			th.f,
			th.oc,
			th.kc,
			t.Dest.Cluster.Namespace,
			"admin")
		err := archiver.Unarchive()
		if err != nil {
			reqLog.Errorln(err)
			return http.StatusInternalServerError, err
		}
	}

	return http.StatusAccepted, nil
}
