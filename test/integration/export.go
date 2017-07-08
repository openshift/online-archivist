package integration

import (
	"fmt"
	"testing"
	"math/rand"
	"time"

	gm "github.com/onsi/gomega"
	log "github.com/Sirupsen/logrus"

	"github.com/openshift/online/archivist/pkg/archive"

	projectv1 "github.com/openshift/origin/pkg/project/apis/project/v1"
	buildv1 "github.com/openshift/origin/pkg/build/apis/build/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kapi "k8s.io/kubernetes/pkg/api"
)

func getTestProjectName(prefix string) string {
	rand.Seed(time.Now().Unix())
	i := rand.Intn(10000)
	return fmt.Sprintf("%s-%d", prefix, i)
}

func testExport(t *testing.T, h *testHarness) {
	gm.RegisterTestingT(t)

	// TODO: delete this learning code
	gm.Expect("a").To(gm.Equal("a"))
	projects, err := h.oc.Projects().List(metav1.ListOptions{})
	gm.Expect(err).NotTo(gm.HaveOccurred())
	for _, p := range projects.Items {
		log.Info(p.Name)
	}

	pn := getTestProjectName("exporttest")
	plog := log.WithFields(log.Fields{
		"project": pn,
	})

	testProject := &projectv1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pn,
			Namespace: "",
			Annotations: map[string]string{
			},
		},
	}
	testProject, err = h.pc.ProjectV1().Projects().Create(testProject)
	if err != nil {
		t.Fatal("error creating project:", err)
	}
	plog.Info("created test project")
	defer h.pc.ProjectV1().Projects().Delete(pn, &metav1.DeleteOptions{})

	buildConfig := &buildv1.BuildConfig{}
	buildConfig.Spec.RunPolicy = buildv1.BuildRunPolicyParallel
	buildConfig.GenerateName = "mybc"
	buildStrategy := buildv1.BuildStrategy{}
	buildStrategy.DockerStrategy = &buildv1.DockerBuildStrategy{}
	buildConfig.Spec.Strategy = buildStrategy
	buildConfig.Spec.Source.Git = &buildv1.GitBuildSource{URI: "example.org"}
	buildConfig, err = h.bc.BuildV1().BuildConfigs(pn).Create(buildConfig)
	if err != nil {
		t.Fatal("error creating build config:", err)
	}

	dc := deploymentConfig(1, pn)
	dc, err = h.deployClient.AppsV1().DeploymentConfigs(pn).Create(dc)
	if err != nil {
		t.Fatal("error creating deployment config:", err)
	}

	a := archive.NewArchiver(h.pc, h.ac, h.uc, h.uidmc, h.idc,
		h.clientFactory, h.oc, h.kc, pn, "user")
	objects, err := a.Export()
	gm.Expect(err).NotTo(gm.HaveOccurred())
	gm.Expect(len(objects.Items)).To(gm.Equal(3))
}

func findObj(list *kapi.List, kind string, name string) runtime.Object {
	/*
	for _, o := range list.Items {
	}
	*/
	return nil

}
