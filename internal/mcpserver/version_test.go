package mcpserver

import (
	"runtime"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

func TestInitializeIdentifiesTheRunningBinary(t *testing.T) {
	stipulate.Covers(t, "REQ-cli-version")
	session, _ := harness(t, nil)
	info := session.InitializeResult().ServerInfo
	if info == nil || info.Name != "stipulator" {
		t.Fatalf("server identity: %+v", info)
	}
	for _, fact := range []string{"revision=", "modified=", "go=", "gofresh=", runtime.Version()} {
		if !strings.Contains(info.Version, fact) {
			t.Fatalf("initialize version lacks %q: %q", fact, info.Version)
		}
	}
}
