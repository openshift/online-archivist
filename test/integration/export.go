package integration

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/openshift/online-archivist/pkg/archive"
	"github.com/openshift/online-archivist/pkg/util"

	imagev1 "github.com/openshift/origin/pkg/image/apis/image/v1"
	projectv1 "github.com/openshift/origin/pkg/project/apis/project/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kapiv1 "k8s.io/kubernetes/pkg/api/v1"
	"k8s.io/kubernetes/pkg/printers"
)

const (
	localImageStreamName = "localimg"
)

func getTestProjectName(prefix string) string {
	rand.Seed(time.Now().Unix())
	i := rand.Intn(10000)
	return fmt.Sprintf("%s-%d", prefix, i)
}

func testExportProjectDoesNotExist(t *testing.T, h *testHarness) {
	a := archive.NewArchiver(h.pc, h.ac, h.uc, h.uidmc, h.idc,
		h.clientFactory, h.oc, h.kc, "nosuchproject", "user")
	_, err := a.Export()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "not found")
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
	pn := getTestProjectName("exporttest")
	log.SetLevel(log.DebugLevel)
	tlog := log.WithFields(log.Fields{
		"namespace": pn,
		"test":      "exporttest",
	})

	createTestProject(t, h, tlog, pn)
	defer h.pc.ProjectV1().Projects().Delete(pn, &metav1.DeleteOptions{})

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
	h.createRegistryImageStream(t, pn, localImageStreamName)
	h.createExternalImageStream(t, pn, "postgresql")
	h.createDeploymentConfig(t, pn, "testdc", localImageStreamName)
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
	assert.Nil(t, err)
	objList := list.(*kapiv1.List)
	logAll(tlog, a, objList)

	t.Run("ExpectedObjectsFound", func(t *testing.T) {
		assert.Len(t, objList.Items, len(expected), "expected object count mismatch")
		for _, s := range expected {
			tokens := strings.Split(s, "/")
			kind, name := tokens[0], tokens[1]
			o := findObj(t, a, objList, kind, name)
			assert.NotNil(t, o, "object was not exported: %s", s)
		}
	})

	t.Run("ExportedObjectsAreVersioned", func(t *testing.T) {
		// May not be the best way to test if a runtime.Object is "versioned", but this
		// is exactly how we serialize so very good coverage that the end result is what
		// we expect.
		p := printers.YAMLPrinter{}
		for _, obj := range objList.Items {
			buf := new(bytes.Buffer)
			err = p.PrintObj(obj.Object, buf)
			if err != nil {
				assert.NotNil(t, err)
			}
			assert.Contains(t, buf.String(), "apiVersion: v1")
		}
	})

	t.Run("ExportedObjectsHaveClusterMetadataStripped", func(t *testing.T) {
		for _, obj := range objList.Items {
			accessor, err := meta.Accessor(obj.Object)
			assert.NoError(t, err)
			assert.Zero(t, accessor.GetUID())
			assert.Zero(t, accessor.GetNamespace())
			assert.Zero(t, accessor.GetCreationTimestamp())
			assert.Zero(t, accessor.GetDeletionTimestamp())
			assert.Zero(t, accessor.GetResourceVersion())
			assert.Zero(t, accessor.GetSelfLink())
		}
	})

	t.Run("ExportedBuilderSAHasCustomDockercfgSecret", func(t *testing.T) {
		bsao := findObj(t, a, objList, "ServiceAccount", "builder")
		ebsa := bsao.(*kapiv1.ServiceAccount)
		assert.Len(t, ebsa.ImagePullSecrets, 1)
		assert.Equal(t, "dockerbuildsecret", ebsa.ImagePullSecrets[0].Name)
	})

	t.Run("ExportedClusterIpIsCleared", func(t *testing.T) {
		so := findObj(t, a, objList, "Service", "testservice")
		eso := so.(*kapiv1.Service)
		assert.Zero(t, eso.Spec.ClusterIP, "cluster IP is not empty")
	})

	t.Run("ExportedImageStreamsHaveNoStatus", func(t *testing.T) {
		imgStreamObj := findObj(t, a, objList, "ImageStream", "localimg")
		is := imgStreamObj.(*imagev1.ImageStream)
		assert.Zero(t, is.Status.DockerImageRepository)
		assert.Len(t, is.Status.Tags, 0)

		imgStreamObj = findObj(t, a, objList, "ImageStream", "postgresql")
		is = imgStreamObj.(*imagev1.ImageStream)
		assert.Zero(t, is.Status.DockerImageRepository)
		assert.Len(t, is.Status.Tags, 0)
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
	assert.NoError(t, err)

	// Now recreate the project with the same name. Deletion already deferred before.
	createTestProject(t, h, tlog, pn)

	t.Run("ImportIntoSameProject", func(t *testing.T) {
		// Proceed to run import tests using the data exported above:

		a := archive.NewArchiver(h.pc, h.ac, h.uc, h.uidmc, h.idc,
			h.clientFactory, h.oc, h.kc, pn, "user")

		yamlStr, err := archive.SerializeObjList(objList)
		assert.NoError(t, err)
		err = a.Import(yamlStr)
		assert.NoError(t, err)
	})

	t.Run("ImportedBuilderSAHasCustomDockercfgSecret", func(t *testing.T) {
		// look up build runtime.Object from the server here
		// bsao := findObj(t, a, objList, "ServiceAccount", "builder")
		bsa, err := h.kc.CoreV1().ServiceAccounts(pn).Get("builder", metav1.GetOptions{})
		if err != nil {
			assert.Len(t, bsa.ImagePullSecrets, 1)
			assert.Equal(t, "dockerbuildsecret", bsa.ImagePullSecrets[0].Name)
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
