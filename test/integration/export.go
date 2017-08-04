package integration

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"
	gm "github.com/onsi/gomega"

	"github.com/openshift/online-archivist/pkg/archive"

	projectv1 "github.com/openshift/origin/pkg/project/apis/project/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kapiv1 "k8s.io/kubernetes/pkg/api/v1"
	"k8s.io/kubernetes/pkg/printers"
)

func getTestProjectName(prefix string) string {
	rand.Seed(time.Now().Unix())
	i := rand.Intn(10000)
	return fmt.Sprintf("%s-%d", prefix, i)
}

func testExportProjectDoesNotExist(t *testing.T, h *testHarness) {
	gm.RegisterTestingT(t)
	a := archive.NewArchiver(h.pc, h.ac, h.uc, h.uidmc, h.idc,
		h.clientFactory, h.oc, h.kc, "nosuchproject", "user")
	_, err := a.Export()
	gm.Expect(err).NotTo(gm.BeNil())
	gm.Expect(err.Error()).Should(gm.ContainSubstring("not found"))
}

func testExport(t *testing.T, h *testHarness) {
	gm.RegisterTestingT(t)
	pn := getTestProjectName("exporttest")
	log.SetLevel(log.DebugLevel)
	tlog := log.WithFields(log.Fields{
		"namespace": pn,
		"test":      "exporttest",
	})

	testProject := &projectv1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pn,
			Namespace:   "",
			Annotations: map[string]string{},
		},
	}
	var err error
	testProject, err = h.pc.ProjectV1().Projects().Create(testProject)
	if err != nil {
		t.Fatal("error creating project:", err)
	}
	tlog.Info("created test project")
	defer h.pc.ProjectV1().Projects().Delete(pn, &metav1.DeleteOptions{})

	h.createDeploymentConfig(t, pn, "testdc")
	h.createSecret(t, pn, "testsecret")

	buildSecret := h.createBuildSecret(t, pn, "dockerbuildsecret")

	// Add the build secret to the default builder SA. Note that these service accounts may take
	// a few seconds to appear after the project is created.
	err = retry(10, 500*time.Millisecond, tlog, func() (err error) {
		bsa, err := h.kc.CoreV1().ServiceAccounts(pn).Get("builder", metav1.GetOptions{})
		if err != nil {
			return
		}
		bsa.ImagePullSecrets = append(bsa.ImagePullSecrets, kapiv1.LocalObjectReference{buildSecret.GetName()})
		_, err = h.kc.CoreV1().ServiceAccounts(pn).Update(bsa)
		if err != nil {
			return
		}
		return
	})
	if err != nil {
		t.Fatalf("error updating builder service accont: %s", err)
	}

	h.createBuildConfig(t, pn, "testbc")
	// We do not expect to see this in the results:
	h.createBuild(t, pn)
	h.createSvcAccount(t, pn, "testserviceaccount")
	h.createRegistryImageStream(t, pn, "integratedregistry")
	h.createExternalImageStream(t, pn, "postgresql")
	h.createService(t, pn, "testservice")

	expected := []string{
		"BuildConfig/testbc",
		"DeploymentConfig/testdc",
		"Secret/testsecret",
		"Secret/dockerbuildsecret",
		"ServiceAccount/testserviceaccount",
		"ServiceAccount/builder",
		"ServiceAccount/deployer",
		"ServiceAccount/default",
		"ImageStream/integratedregistry",
		"ImageStream/postgresql",
		"Service/testservice",
	}

	a := archive.NewArchiver(h.pc, h.ac, h.uc, h.uidmc, h.idc,
		h.clientFactory, h.oc, h.kc, pn, "user")
	list, err := a.Export()
	gm.Expect(err).NotTo(gm.HaveOccurred())
	objList := list.(*kapiv1.List)
	logAll(tlog, a, objList)

	t.Run("ExpectedObjectsFound", func(t *testing.T) {
		gm.RegisterTestingT(t)
		gm.Expect(len(objList.Items)).To(gm.Equal(len(expected)),
			"expected object count mismatch")
		for _, s := range expected {
			tokens := strings.Split(s, "/")
			kind, name := tokens[0], tokens[1]
			o := findObj(t, a, objList, kind, name)
			gm.Expect(o).NotTo(gm.BeNil(), "object was not exported: %s", s)
		}
	})

	t.Run("ExportedObjectsAreVersioned", func(t *testing.T) {
		gm.RegisterTestingT(t)
		// May not be the best way to test if a runtime.Object is "versioned", but this
		// is exactly how we serialize so very good coverage that the end result is what
		// we expect.
		p := printers.YAMLPrinter{}
		for _, obj := range objList.Items {
			buf := new(bytes.Buffer)
			err = p.PrintObj(obj.Object, buf)
			if err != nil {
				gm.Expect(err).NotTo(gm.BeNil())
			}
			gm.Expect(buf.String()).To(gm.ContainSubstring("apiVersion: v1"))
		}
	})

	// TODO: we may need more logic and testing around image streams, which should be exported and
	// how well they work. However we need to get further with import to test how things behave.

	// TODO: make sure cluster info is stripped from objects
	t.Run("ExportedObjectsHaveClusterMetadataStripped", func(t *testing.T) {
		gm.RegisterTestingT(t)
		for _, obj := range objList.Items {
			accessor, err := meta.Accessor(obj.Object)
			gm.Expect(err).NotTo(gm.HaveOccurred())
			gm.Expect(accessor.GetUID()).To(gm.BeZero(), "%s has UID set", accessor.GetName())
			gm.Expect(accessor.GetNamespace()).To(gm.BeZero(), "%s has namespace set", accessor.GetName())
			gm.Expect(accessor.GetCreationTimestamp()).To(gm.BeZero(), "%s has creation time set", accessor.GetName())
			gm.Expect(accessor.GetDeletionTimestamp()).To(gm.BeNil(), "%s has deletion time set", accessor.GetName())
			gm.Expect(accessor.GetResourceVersion()).To(gm.BeZero(), "%s has resource version set", accessor.GetName())
			gm.Expect(accessor.GetSelfLink()).To(gm.BeZero(), "%s has self link set", accessor.GetName())
		}
	})

	t.Run("ExportedBuilderSAHasCustomDockercfgSecret", func(t *testing.T) {
		gm.RegisterTestingT(t)
		bsao := findObj(t, a, objList, "ServiceAccount", "builder")
		ebsa := bsao.(*kapiv1.ServiceAccount)
		gm.Expect(len(ebsa.ImagePullSecrets)).To(gm.Equal(1))
		gm.Expect(ebsa.ImagePullSecrets[0].Name).To(gm.Equal("dockerbuildsecret"))
	})

	t.Run("ExportedClusterIpIsCleared", func(t *testing.T) {
		gm.RegisterTestingT(t)
		so := findObj(t, a, objList, "Service", "testservice")
		eso := so.(*kapiv1.Service)
		gm.Expect(len(eso.Spec.ClusterIP)).To(gm.Equal(0))
		gm.Expect(eso.Spec.ClusterIP).To(gm.Equal(""), "cluster is is not empty")
	})
}

// findObj finds an object of the given kind and name. If not found it will return nil.
func findObj(t *testing.T, a *archive.Archiver, list *kapiv1.List, kind string, name string) runtime.Object {
	for _, ro := range list.Items {
		o := ro.Object
		if md, err := metav1.ObjectMetaFor(o); err == nil {
			if a.ObjKind(o) == kind && md.Name == name {
				return o
			}
		} else {
			t.Fatalf("error loading ObjectMeta for: %s", o)
			return nil
		}
	}
	return nil
}

func logAll(tlog *log.Entry, a *archive.Archiver, list *kapiv1.List) {
	tlog.Infoln("object list:")
	for _, o := range list.Items {
		if md, err := metav1.ObjectMetaFor(o.Object); err == nil {
			tlog.Infof("   %s/%s", a.ObjKind(o.Object), md.Name)
		} else {
			tlog.Errorf("error loading ObjectMeta for: %s", o)
		}
	}
}
