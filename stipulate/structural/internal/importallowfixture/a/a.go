package a

import (
	"fmt"

	"github.com/greatliontech/go-x-tools/go/packages"
	"github.com/greatliontech/stipulator/stipulate/structural/internal/importallowfixture/b"
)

var Value = fmt.Sprint(b.Value, packages.NeedName)
