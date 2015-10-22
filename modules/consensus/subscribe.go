package consensus

import (
	"errors"

	"github.com/boltdb/bolt"

	"github.com/NebulousLabs/Sia/build"
	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/types"
)

// A changeEntry records a change to the consensus set that happened, and is
// used during subscriptions.
type changeEntry struct {
	revertedBlocks []types.BlockID
	appliedBlocks  []types.BlockID
}

// computeConsensusChange computes the consensus change from the change entry
// at index 'i' in the change log. If i is out of bounds, an error is returned.
func (cs *ConsensusSet) computeConsensusChange(tx *bolt.Tx, i int) (cc modules.ConsensusChange, err error) {
	if i < 0 || i >= len(cs.changeLog) {
		err = errors.New("bounds error when querying changelog")
		return
	}

	for _, revertedBlockID := range cs.changeLog[i].revertedBlocks {
		revertedBlock, err := getBlockMap(tx, revertedBlockID)
		if build.DEBUG && err != nil {
			panic(err)
		}

		// Because the direction is 'revert', the order of the diffs needs to
		// be flipped and the direction of the diffs also needs to be flipped.
		cc.RevertedBlocks = append(cc.RevertedBlocks, revertedBlock.Block)
		for i := len(revertedBlock.SiacoinOutputDiffs) - 1; i >= 0; i-- {
			scod := revertedBlock.SiacoinOutputDiffs[i]
			scod.Direction = !scod.Direction
			cc.SiacoinOutputDiffs = append(cc.SiacoinOutputDiffs, scod)
		}
		for i := len(revertedBlock.FileContractDiffs) - 1; i >= 0; i-- {
			fcd := revertedBlock.FileContractDiffs[i]
			fcd.Direction = !fcd.Direction
			cc.FileContractDiffs = append(cc.FileContractDiffs, fcd)
		}
		for i := len(revertedBlock.SiafundOutputDiffs) - 1; i >= 0; i-- {
			sfod := revertedBlock.SiafundOutputDiffs[i]
			sfod.Direction = !sfod.Direction
			cc.SiafundOutputDiffs = append(cc.SiafundOutputDiffs, sfod)
		}
		for i := len(revertedBlock.DelayedSiacoinOutputDiffs) - 1; i >= 0; i-- {
			dscod := revertedBlock.DelayedSiacoinOutputDiffs[i]
			dscod.Direction = !dscod.Direction
			cc.DelayedSiacoinOutputDiffs = append(cc.DelayedSiacoinOutputDiffs, dscod)
		}
		for i := len(revertedBlock.SiafundPoolDiffs) - 1; i >= 0; i-- {
			sfpd := revertedBlock.SiafundPoolDiffs[i]
			sfpd.Direction = modules.DiffRevert
			cc.SiafundPoolDiffs = append(cc.SiafundPoolDiffs, sfpd)
		}
	}
	for _, appliedBlockID := range cs.changeLog[i].appliedBlocks {
		appliedBlock, err := getBlockMap(tx, appliedBlockID)
		if build.DEBUG && err != nil {
			panic(err)
		}

		cc.AppliedBlocks = append(cc.AppliedBlocks, appliedBlock.Block)
		for _, scod := range appliedBlock.SiacoinOutputDiffs {
			cc.SiacoinOutputDiffs = append(cc.SiacoinOutputDiffs, scod)
		}
		for _, fcd := range appliedBlock.FileContractDiffs {
			cc.FileContractDiffs = append(cc.FileContractDiffs, fcd)
		}
		for _, sfod := range appliedBlock.SiafundOutputDiffs {
			cc.SiafundOutputDiffs = append(cc.SiafundOutputDiffs, sfod)
		}
		for _, dscod := range appliedBlock.DelayedSiacoinOutputDiffs {
			cc.DelayedSiacoinOutputDiffs = append(cc.DelayedSiacoinOutputDiffs, dscod)
		}
		for _, sfpd := range appliedBlock.SiafundPoolDiffs {
			cc.SiafundPoolDiffs = append(cc.SiafundPoolDiffs, sfpd)
		}
	}
	return
}

// readlockUpdateSubscribers will inform all subscribers of a new update to the
// consensus set. The call must be made with a demoted lock or a readlock.
// readlockUpdateSubscribers does not alter the changelog, the changelog must
// be updated beforehand.
func (cs *ConsensusSet) readlockUpdateSubscribers(ce changeEntry) {
	// Get the consensus change and send it to all subscribers.
	var cc modules.ConsensusChange
	err := cs.db.View(func(tx *bolt.Tx) error {
		var err error
		cc, err = cs.computeConsensusChange(tx, len(cs.changeLog)-1)
		return err
	})
	if err != nil && build.DEBUG {
		panic(err)
	}
	for _, subscriber := range cs.subscribers {
		subscriber.ProcessConsensusChange(cc)
	}

	// Get the change digest and send it to all subscribers.
	var revertedIDs, appliedIDs []types.BlockID
	for _, rb := range cc.RevertedBlocks {
		revertedIDs = append(revertedIDs, rb.ID())
	}
	for _, ab := range cc.AppliedBlocks {
		appliedIDs = append(appliedIDs, ab.ID())
	}
	for _, ds := range cs.digestSubscribers {
		ds.ProcessConsensusDigest(revertedIDs, appliedIDs)
	}
}

// ConsensusChange returns the consensus change that occured at index 'i',
// returning an error if the input is out of bounds. For example,
// ConsensusChange(5) will return the 6th consensus change that was issued to
// subscribers. ConsensusChanges can be assumed to be consecutive.
func (cs *ConsensusSet) ConsensusChange(i int) (cc modules.ConsensusChange, err error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	err = cs.db.View(func(tx *bolt.Tx) error {
		cc, err = cs.computeConsensusChange(tx, i)
		return err
	})
	if err != nil {
		return modules.ConsensusChange{}, err
	}
	return cc, nil
}

// ConsensusSetDigestSubscribe accepts a new digest subscriber who will receive
// a call to ProcessConsensusDigest every time there is a change in the
// consensus set.
func (cs *ConsensusSet) ConsensusSetDigestSubscribe(subscriber modules.ConsensusSetDigestSubscriber) {
	cs.mu.Lock()
	cs.digestSubscribers = append(cs.digestSubscribers, subscriber)
	cs.mu.Demote()
	defer cs.mu.DemotedUnlock()

	var currentPath []types.BlockID
	err := cs.db.View(func(tx *bolt.Tx) error {
		// Get the whole current path into memory to be sent as the first
		// digest.
		//
		// TODO: Change this construction to something simpler.
		height := blockHeight(tx)
		for i := types.BlockHeight(0); i <= height; i++ {
			id, err := getPath(tx, i)
			if err != nil {
				return err
			}
			currentPath = append(currentPath, id)
		}
		subscriber.ProcessConsensusDigest(nil, currentPath)
		return nil
	})
	if build.DEBUG && err != nil {
		panic(err)
	}
}

// ConsensusSetSubscribe accepts a new subscriber who will receive a call to
// ProcessConsensusChange every time there is a change in the consensus set.
func (cs *ConsensusSet) ConsensusSetSubscribe(subscriber modules.ConsensusSetSubscriber) {
	cs.mu.Lock()
	cs.subscribers = append(cs.subscribers, subscriber)
	cs.mu.Demote()
	defer cs.mu.DemotedUnlock()

	err := cs.db.View(func(tx *bolt.Tx) error {
		for i := range cs.changeLog {
			cc, err := cs.computeConsensusChange(tx, i)
			if err != nil && build.DEBUG {
				panic(err)
			}
			subscriber.ProcessConsensusChange(cc)
		}
		return nil
	})
	if build.DEBUG && err != nil {
		panic(err)
	}
}