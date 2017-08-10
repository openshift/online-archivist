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
	"github.com/openshift/online-archivist/pkg/util"

	imagev1 "github.com/openshift/origin/pkg/image/apis/image/v1"
	projectv1 "github.com/openshift/origin/pkg/project/apis/project/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func createTestProject(t *testing.T, h *testHarness, tlog *log.Entry, pn string) *projectv1.Project {
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
	return testProject
}

func testExport(t *testing.T, h *testHarness) {
	gm.RegisterTestingT(t)
	pn := getTestProjectName("exporttest")
	log.SetLevel(log.DebugLevel)
	tlog := log.WithFields(log.Fields{
		"namespace": pn,
		"test":      "exporttest",
	})

	createTestProject(t, h, tlog, pn)
	defer h.pc.ProjectV1().Projects().Delete(pn, &metav1.DeleteOptions{})

	h.createDeploymentConfig(t, pn, "testdc")
	h.createSecret(t, pn, "testsecret")

	buildSecret := h.createBuildSecret(t, pn, "dockerbuildsecret")

	// Add the build secret to the default builder SA. Note that these service accounts may take
	// a few seconds to appear after the project is created.
	err := util.Retry(10, 500*time.Millisecond, tlog, func() (err error) {
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
	h.createRegistryImageStream(t, pn, "localimg")
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
		"ImageStream/localimg",
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
		gm.Expect(len(eso.Spec.ClusterIP)).Should(gm.BeZero())
		gm.Expect(eso.Spec.ClusterIP).To(gm.Equal(""), "cluster IP is not empty")
	})

	t.Run("ExportedImageStreamsHaveNoStatus", func(t *testing.T) {
		gm.RegisterTestingT(t)
		imgStreamObj := findObj(t, a, objList, "ImageStream", "localimg")
		is := imgStreamObj.(*imagev1.ImageStream)
		gm.Expect(is.Status.DockerImageRepository).To(gm.BeZero())
		gm.Expect(len(is.Status.Tags)).To(gm.Equal(0))

		imgStreamObj = findObj(t, a, objList, "ImageStream", "postgresql")
		is = imgStreamObj.(*imagev1.ImageStream)
		gm.Expect(is.Status.DockerImageRepository).To(gm.BeZero())
		gm.Expect(len(is.Status.Tags)).To(gm.Equal(0))
	})

	// At this point we want to proceed to import testing. Per our current target of
	// first being able to import back into the same project name and cluster (leaving
	// an empty project around after archival), we need to clean out the project.
	// TODO: hookup project cleanup here, for now we'll just delete it entirely and recreate:
	h.pc.ProjectV1().Projects().Delete(pn, &metav1.DeleteOptions{})

	// Wait for project deletion to complete, can rountinely take >20s.
	tlog.Info("waiting for project termination to complete")
	err = util.Retry(12, 5*time.Second, tlog, func() (err error) {
		proj, err := h.pc.ProjectV1().Projects().Get(pn, metav1.GetOptions{})
		// No error indicates project still exists, so return an error. (yes this is weird I know)
		if err == nil {
			return fmt.Errorf("project still exists, status: %v", proj.Status)
		}
		// Error returned, should indicate project no longer exists:
		tlog.Info("project lookup error should indicate project on longer exists: %s", err)
		return nil
	})
	gm.Expect(err).NotTo(gm.HaveOccurred())

	// Now recreate the project with the same name. Deletion already deferred before.
	createTestProject(t, h, tlog, pn)

	t.Run("ImportIntoSameProject", func(t *testing.T) {
		// Proceed to run import tests using the data exported above:
		gm.RegisterTestingT(t)

		a := archive.NewArchiver(h.pc, h.ac, h.uc, h.uidmc, h.idc,
			h.clientFactory, h.oc, h.kc, pn, "user")

		yamlStr, err := archive.SerializeObjList(objList)
		gm.Expect(err).NotTo(gm.HaveOccurred())
		err = a.Import(yamlStr)
		gm.Expect(err).NotTo(gm.HaveOccurred())

		// TODO: add subtests here to validate all the things, just checking error above is not sufficient.
		// i.e. check expected objects exist, check imgPullSecrets made it onto the default SA, etc.
	})

	t.Run("ImportedBuilderSAHasCustomDockercfgSecret", func(t *testing.T) {
		gm.RegisterTestingT(t)
		// look up build runtime.Object from the server here
		// bsao := findObj(t, a, objList, "ServiceAccount", "builder")
		bsa, err := h.kc.CoreV1().ServiceAccounts(pn).Get("builder", metav1.GetOptions{})
		if err != nil {
			gm.Expect(len(bsa.ImagePullSecrets)).To(gm.Equal(1))
			gm.Expect(bsa.ImagePullSecrets[0].Name).To(gm.Equal("dockerbuildsecret"))
		}
	})
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
