package witnesscache

import (
	"os"
	"testing"

	"github.com/greatliontech/stipulator/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.IsolateMain(m))
}
