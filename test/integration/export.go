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
	//buildv1 "github.com/openshift/origin/pkg/build/apis/build/v1"

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
	pn := getTestProjectName("exporttest")
	log.SetLevel(log.DebugLevel)
	tlog := log.WithFields(log.Fields{
		"project": pn,
		"test": "exporttest",
	})

	testProject := &projectv1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pn,
			Namespace: "",
			Annotations: map[string]string{
			},
		},
	}
	var err error
	testProject, err = h.pc.ProjectV1().Projects().Create(testProject)
	if err != nil {
		t.Fatal("error creating project:", err)
	}
	tlog.Info("created test project")
	defer h.pc.ProjectV1().Projects().Delete(pn, &metav1.DeleteOptions{})

	bc := buildConfig("mybc-")
	bc, err = h.bc.BuildV1().BuildConfigs(pn).Create(bc)
	if err != nil {
		t.Fatal("error creating build config:", err)
	}

	dc := deploymentConfig(1, pn)
	dc, err = h.deployClient.AppsV1().DeploymentConfigs(pn).Create(dc)
	if err != nil {
		t.Fatal("error creating deployment config:", err)
	}

	s := secret(pn,"testsecret")
	s, err = h.kc.Core().Secrets(pn).Create(s)
	if err != nil {
		t.Fatal("error creating secret:", err)
	}

	a := archive.NewArchiver(h.pc, h.ac, h.uc, h.uidmc, h.idc,
		h.clientFactory, h.oc, h.kc, pn, "user")
	objects, err := a.Export()
	logAll(tlog, a, objects)
	gm.Expect(err).NotTo(gm.HaveOccurred())

	// TODO: assert a specific list of object kind/name combos
	gm.Expect(len(objects.Items)).To(gm.Equal(4))

	/*
	bcResult := findObj(t, a, objects, "BuildConfig", buildConfig.Name)
	tlog.Info("Found build", bcResult)
	dcResult := findObj(t, a, objects, "DeploymentConfig", dc.Name)
	tlog.Info("Found dc", dcResult)
	*/
	secretResult := findObj(t, a, objects, "Secret", s.Name)
	tlog.Info("Found secret", secretResult)
	gm.Expect(secretResult).NotTo(gm.BeNil())
	// TODO: filter secrets for service accounts
	// TODO: make sure cluster info is stripped from objects
}

// findObj finds an object of he given kind and name. If not found it will error the test.
func findObj(t *testing.T, a *archive.Archiver, list *kapi.List, kind string, name string) runtime.Object {
	for _, o := range list.Items {
		if meta, err := metav1.ObjectMetaFor(o); err == nil {
			if a.ObjKind(o) == kind && meta.Name == name {
				return o
			}
		} else {
			t.Fatalf("error loading ObjectMeta for: %s", o)
			return nil
		}
	}
	// Fail the test if we can't find the requested object.
	t.Fatalf("unable to find object %s/%s", kind, name)
	return nil
}

func logAll(tlog *log.Entry, a *archive.Archiver,  list *kapi.List) {
	tlog.Infoln("object list:")
	for _, o := range list.Items {
		if meta, err := metav1.ObjectMetaFor(o); err == nil {
			tlog.Infof("   %s/%s", a.ObjKind(o), meta.Name)
		} else {
			tlog.Errorf("error loading ObjectMeta for: %s", o)
		}
	}
}
