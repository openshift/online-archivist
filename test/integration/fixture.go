package integration

import (
	"testing"
	"strings"

	"github.com/openshift/online/archivist/cmd"

	authclientset "github.com/openshift/origin/pkg/authorization/generated/clientset"
	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"
	buildclientset "github.com/openshift/origin/pkg/build/generated/clientset"
	userclientset "github.com/openshift/origin/pkg/user/generated/clientset"
	deployclientset "github.com/openshift/origin/pkg/deploy/generated/clientset"
	buildv1 "github.com/openshift/origin/pkg/build/apis/build/v1"

	deployv1 "github.com/openshift/origin/pkg/deploy/apis/apps/v1"

	restclient "k8s.io/client-go/rest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	kapiv1 "k8s.io/kubernetes/pkg/api/v1"
	kapi "k8s.io/kubernetes/pkg/api"

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

func podTemplateSpec() *kapiv1.PodTemplateSpec {
	return &kapiv1.PodTemplateSpec{
		Spec: kapiv1.PodSpec{
			Containers: []kapiv1.Container{
				{
					Name:  "container1",
					Image: "registry:8080/repo1:ref1",
					Env: []kapiv1.EnvVar{
						{
							Name:  "ENV1",
							Value: "VAL1",
						},
					},
					ImagePullPolicy:          kapiv1.PullIfNotPresent,
					TerminationMessagePath:   "/dev/termination-log",
					TerminationMessagePolicy: kapiv1.TerminationMessageReadFile,
				},
				{
					Name:                     "container2",
					Image:                    "registry:8080/repo1:ref2",
					ImagePullPolicy:          kapiv1.PullIfNotPresent,
					TerminationMessagePath:   "/dev/termination-log",
					TerminationMessagePolicy: kapiv1.TerminationMessageReadFile,
				},
			},
			RestartPolicy: kapiv1.RestartPolicyAlways,
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"a": "b"},
		},
	}
}

func secret(projectName string, name string) *kapi.Secret {
	return &kapi.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: projectName,
		},
		Data: map[string][]byte{
			"foo": []byte("foo"),
			"bar": []byte("bar"),
		},
	}
}

func buildConfig(buildPrefix string) *buildv1.BuildConfig {
	buildConfig := &buildv1.BuildConfig{}
	buildConfig.Spec.RunPolicy = buildv1.BuildRunPolicyParallel
	buildConfig.GenerateName = buildPrefix
	buildStrategy := buildv1.BuildStrategy{}
	buildStrategy.DockerStrategy = &buildv1.DockerBuildStrategy{}
	buildConfig.Spec.Strategy = buildStrategy
	buildConfig.Spec.Source.Git = &buildv1.GitBuildSource{URI: "example.org"}
	return buildConfig
}
