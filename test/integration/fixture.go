package integration

import (
	"fmt"
	"testing"
	"time"
	"strings"

	"github.com/openshift/online/archivist/cmd"

	authclientset "github.com/openshift/origin/pkg/authorization/generated/clientset"
	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"
	buildclientset "github.com/openshift/origin/pkg/build/generated/clientset"
	userclientset "github.com/openshift/origin/pkg/user/generated/clientset"
	deployclientset "github.com/openshift/origin/pkg/deploy/generated/clientset"

	deployv1 "github.com/openshift/origin/pkg/deploy/apis/apps/v1"

	restclient "k8s.io/client-go/rest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	kapi "k8s.io/kubernetes/pkg/api/v1"

	"github.com/spf13/pflag"
)

type testHarness struct {
	oc osclient.Interface
	kc kclientset.Interface
	restConfig *restclient.Config
	clientFactory *clientcmd.Factory

	pc projectclientset.Interface
	ac authclientset.Interface
	uc userclientset.Interface
	bc buildclientset.Interface
	deployClient deployclientset.Interface

	// TODO: Legacy client usage here until we find their equivalent in new generated clientsets:
	uidmc osclient.UserIdentityMappingInterface
	idc   osclient.IdentityInterface
}

func username() string {
	return fmt.Sprintf("%s%d", "test", time.Now().UnixNano())
}

func newTestHarness(t *testing.T) *testHarness {

	// Use default config which defaults to using current kubeconfig context. For our purposes we
	// assume you must be logged in as system:admin to a test cluster, likely minishift or oc cluster up.
	// If not, we immediately fail the test case and tell you why. In future, it would be nice to
	// specify how to connect to a test cluster via config or env vars.
	//
	// In general the tests should clean up after themselves.
	dcc := clientcmd.DefaultClientConfig(pflag.NewFlagSet("empty", pflag.ContinueOnError))
	rawc, err := dcc.RawConfig()
	if err != nil {
		t.Errorf("unable to parse kubeconfig")
		t.FailNow()
	}
	if !strings.Contains(rawc.CurrentContext, "system:admin") {
		t.Errorf("must oc login to a test cluster as 'system:admin', current context was: %s",
			rawc.CurrentContext)
		t.FailNow()
	}
	restConfig, f, oc, kc, err := cmd.CreateClientsForConfig(dcc)
	if err != nil {
		t.Fatal(err)
	}

	pc, ac, uc, uidmc, idc := cmd.CreateOpenshiftAPIClients(restConfig, oc)
	bc, err := buildclientset.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	deployClient, err := deployclientset.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}

	return &testHarness{
		oc: oc,
		kc: kc,
		restConfig: restConfig,
		clientFactory: f,

		pc: pc,
		ac: ac,
		uc: uc,
		uidmc: uidmc,
		idc: idc,

		bc: bc,
		deployClient: deployClient,
	}
}

func deploymentConfig(version int64, projectName string) *deployv1.DeploymentConfig {
	return &deployv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "config",
			Namespace: projectName,
		},
		Spec:   dcSpec(),
		Status: dcStatus(version),
	}
}

func dcSpec() deployv1.DeploymentConfigSpec {
	return deployv1.DeploymentConfigSpec{
		Replicas: 1,
		Selector: map[string]string{"a": "b"},
		Strategy: deployv1.DeploymentStrategy{
			Type: deployv1.DeploymentStrategyTypeRecreate,
		},
		Template: podTemplateSpec(),
	}
}

func dcStatus(version int64) deployv1.DeploymentConfigStatus {
	return deployv1.DeploymentConfigStatus{
		LatestVersion: version,
	}
}

func mkintp(i int) *int64 {
	v := int64(i)
	return &v
}

func podTemplateSpec() *kapi.PodTemplateSpec {
	return &kapi.PodTemplateSpec{
		Spec: kapi.PodSpec{
			Containers: []kapi.Container{
				{
					Name:  "container1",
					Image: "registry:8080/repo1:ref1",
					Env: []kapi.EnvVar{
						{
							Name:  "ENV1",
							Value: "VAL1",
						},
					},
					ImagePullPolicy:          kapi.PullIfNotPresent,
					TerminationMessagePath:   "/dev/termination-log",
					TerminationMessagePolicy: kapi.TerminationMessageReadFile,
				},
				{
					Name:                     "container2",
					Image:                    "registry:8080/repo1:ref2",
					ImagePullPolicy:          kapi.PullIfNotPresent,
					TerminationMessagePath:   "/dev/termination-log",
					TerminationMessagePolicy: kapi.TerminationMessageReadFile,
				},
			},
			RestartPolicy:                 kapi.RestartPolicyAlways,
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"a": "b"},
		},
	}
}
