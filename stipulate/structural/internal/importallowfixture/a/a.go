package a

import (
	"fmt"

	"github.com/greatliontech/stipulator/stipulate/structural/internal/importallowfixture/b"
	"github.com/greatliontech/go-x-tools/go/packages"
)

var Value = fmt.Sprint(b.Value, packages.NeedName)
