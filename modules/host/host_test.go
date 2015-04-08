package host

import (
	"path/filepath"
	"testing"

	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/modules/consensus"
	"github.com/NebulousLabs/Sia/modules/gateway"
	"github.com/NebulousLabs/Sia/modules/tester"
	"github.com/NebulousLabs/Sia/modules/transactionpool"
	"github.com/NebulousLabs/Sia/modules/wallet"
)

// A HostTester contains a consensus tester and a host, and provides a set of
// helper functions for testing the host without needing other modules such as
// the renter.
type HostTester struct {
	*consensus.ConsensusTester
	*Host
}

// CreateHostTester initializes a HostTester.
func CreateHostTester(name string, t *testing.T) (ht *HostTester) {
	ct := consensus.NewTestingEnvironment(t)
	testdir := tester.TempDir("host", name)
	g, err := gateway.New(":0", ct.State, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		t.Fatal(err)
	}

	tp, err := transactionpool.New(ct.State, g)
	if err != nil {
		t.Fatal(err)
	}

	w, err := wallet.New(ct.State, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		t.Fatal(err)
	}

	h, err := New(ct.State, tp, w, filepath.Join(testdir, modules.HostDir))
	if err != nil {
		t.Fatal(err)
	}

	ht = new(HostTester)
	ht.ConsensusTester = ct
	ht.Host = h
	return
}