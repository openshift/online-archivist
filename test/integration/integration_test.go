package integration

import (
	"os"
	"testing"

	log "github.com/Sirupsen/logrus"
)

// TestIntegration is the single entrypoint for integration tests. It uses Go
// subtests because the origin test scaffolding works in terms of a testing.T
// and we don't want to re-initialize the master and etcd instance with every
// individual test. So, create the harness just once and pass it to internal
// tests methods re-exposed as subtests. Remember to add any new tests to this
// list. Tests publicly exposed outside the subtest structure won't have access
// to the harness.
func TestIntegration(t *testing.T) {
	log.SetLevel(log.DebugLevel)
	log.SetOutput(os.Stdout)
	h := newTestHarness(t)

	t.Run("ExportProjectDoesNotExist", func(t *testing.T) { testExportProjectDoesNotExist(t, h) })
	t.Run("ExportSimple", func(t *testing.T) { testExport(t, h) })
}
