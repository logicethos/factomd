package state_test

import (
	//"bytes"
	//"encoding/binary"
	"testing"
	//"time"

	//"github.com/FactomProject/factomd/common/messages"
	"github.com/FactomProject/factomd/common/primitives"
	. "github.com/FactomProject/factomd/state"
	"github.com/FactomProject/factomd/testHelper"
)

func TestIdentity(t *testing.T) {
	s := testHelper.CreateAndPopulateTestState()
	index := CreateBlankFactomIdentity(s, primitives.NewZeroHash())
	if len(s.Identities) == 0 || index != 0 {
		t.Errorf("Failed making blank identity")
	}
}
