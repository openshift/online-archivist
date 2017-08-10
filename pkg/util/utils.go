package util

import (
	"fmt"
	"time"

	log "github.com/Sirupsen/logrus"
)

func StringInSlice(a string, list []string) bool {
	for _, b := range list {

		if b == a {
			return true
		}
	}
	return false
}

func Retry(attempts int, sleep time.Duration, tlog *log.Entry, callback func() error) (err error) {
	for i := 0; ; i++ {
		err = callback()
		if err == nil {
			return
		}

		if i >= (attempts - 1) {
			break
		}

		time.Sleep(sleep)
		tlog.Warnf("retrying after error: %s attempt: %d/%d", err, i+1, attempts)
	}
	return fmt.Errorf("function failed after %d attempts, last error: %s", attempts, err)
}
