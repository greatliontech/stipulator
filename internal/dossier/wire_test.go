package dossier

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestRetiredDossierWireContract(t *testing.T) {
	dossier := (&stipulatorv1.Dossier{}).ProtoReflect().Descriptor()
	if !dossier.ReservedRanges().Has(6) || !dossier.ReservedNames().Has("hardening") {
		t.Fatal("Dossier does not reserve retired hardening field 6")
	}
}
