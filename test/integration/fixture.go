package integration

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/golang/glog"

	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/server/origin"
	oauthapi "github.com/openshift/origin/pkg/oauth/api"
	userapi "github.com/openshift/origin/pkg/user/api"
	testutil "github.com/openshift/origin/test/util"
	testserver "github.com/openshift/origin/test/util/server"

	kapi "k8s.io/kubernetes/pkg/api"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/util/uuid"
)

type testHarness struct {
	config *api.ServerConfig
	oc     osclient.Interface
	kc     kclient.Interface
}

func username() string {
	return fmt.Sprintf("%s%d", "test", time.Now().UnixNano())
}

func newTestHarness(t *testing.T) *testHarness {
	// Start etcd.
	testutil.RequireEtcd(t)

	// Start an OpenShift master. Note that we can't use just the API server
	// because project deletion won't work without controllers (because they are
	// cache based).
	_, kubeConfig, err := testserver.StartTestMaster()
	if err != nil {
		t.Fatal(err)
	}

	// Get some clients for the master.
	oc, err := testutil.GetClusterAdminClient(kubeConfig)
	if err != nil {
		t.Fatal(err)
	}
	kc, err := testutil.GetClusterAdminKubeClient(kubeConfig)
	if err != nil {
		t.Fatal(err)
	}

	return &testHarness{
		config: config,
		oc:     oc,
		kc:     kc,
	}
}

func (h *testHarness) CreateOAuthAccessToken(account *api.Account, user *userapi.User) (*oauthapi.OAuthAccessToken, error) {
	token := &oauthapi.OAuthAccessToken{
		ObjectMeta: kapi.ObjectMeta{
			Name: string(uuid.NewUUID()),
		},
		ClientName:     origin.OpenShiftCLIClientID,
		UserUID:        string(user.UID),
		UserName:       account.Username,
		AuthorizeToken: account.Username,
		RefreshToken:   account.Username,
	}

	return h.oc.OAuthAccessTokens().Create(token)
}
